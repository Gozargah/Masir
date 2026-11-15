package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"time"
)

const (
	probeWorkerArg = "--masir-internal-xray-probe-v1"
	maxProbeInput  = 16 << 20
	maxProbeOutput = 1 << 20
	probeLifetime  = 2 * time.Minute
)

// One helper at a time bounds memory and network load across all callers.
// Each helper still probes up to eight outbounds concurrently.
var probeSlot = make(chan struct{}, 1)

type probeRequest struct {
	Config  []byte
	Probes  [][]byte
	Options LatencyOptions
	Workers int
}

type probeResult struct {
	Index    int
	Duration time.Duration
	Error    string
}

// Re-exec the same binary without entering CLI, service, or GUI startup. Keeping
// dispatch here makes every embedder (including tests) use the same boundary.
// Only the reserved, versioned argument enters this mode; configs travel on stdin,
// never through command arguments, environment variables, or temporary files.
func init() {
	if len(os.Args) == 2 && os.Args[1] == probeWorkerArg {
		// Bound orphan lifetime too, even if the parent exits or core.New hangs.
		time.AfterFunc(probeLifetime, func() { os.Exit(1) })
		if os.Getenv("GOMEMLIMIT") == "" {
			debug.SetMemoryLimit(100 * 1024 * 1024)
		}
		if err := serveProbe(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		// Process exit also releases resources leaked by an unsuccessful core.New.
		os.Exit(0)
	}
}

// MeasureDelay measures a config in an isolated process. Cancellation kills and
// reaps the helper even when Xray initialization or shutdown is stuck.
func MeasureDelay(ctx context.Context, cfgJSON []byte, opt LatencyOptions) (time.Duration, error) {
	var result probeResult
	err := runProbe(ctx, probeRequest{Config: cfgJSON, Options: opt}, 1, func(r probeResult) {
		result = r
	})
	if err != nil {
		return 0, err
	}
	if result.Error != "" {
		return 0, errors.New(result.Error)
	}
	return result.Duration, nil
}

// MeasureEach streams one result per outbound, serially, including cancellation
// and worker failures. A new process per batch contains all Xray global state.
func MeasureEach(ctx context.Context, probes []Probe, opt LatencyOptions, workers int, report func(string, time.Duration, error)) {
	for i := 0; i < len(probes); i += maxProbeBatch {
		batch := probes[i:min(i+maxProbeBatch, len(probes))]
		seen := make([]bool, len(batch))
		raw := make([][]byte, len(batch))
		for j, p := range batch {
			raw[j] = p.Outbound
		}
		err := runProbe(ctx, probeRequest{Probes: raw, Options: opt, Workers: workers}, len(batch), func(r probeResult) {
			seen[r.Index] = true
			var probeErr error
			if r.Error != "" {
				probeErr = errors.New(r.Error)
			}
			report(batch[r.Index].Tag, r.Duration, probeErr)
		})
		for j, p := range batch {
			if !seen[j] {
				report(p.Tag, 0, err)
			}
		}
	}
}

// runProbe owns the complete helper lifecycle; no caller can fall back to an
// in-process instance. Results are bounded and validated before reaching callers.
func runProbe(ctx context.Context, req probeRequest, count int, report func(probeResult)) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case probeSlot <- struct{}{}:
	}
	defer func() { <-probeSlot }()
	ctx, cancel := context.WithTimeout(ctx, probeLifetime)
	defer cancel()
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode probe: %w", err)
	}
	if len(data) > maxProbeInput {
		return errors.New("probe configuration is too large")
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate probe executable: %w", err)
	}
	cmd := exec.CommandContext(ctx, self, probeWorkerArg)
	configureProbeProcess(cmd)
	cmd.Stdin = bytes.NewReader(data)
	// Xray's incidental stderr is discarded: it can contain server credentials.
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open probe results: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start probe: %w", err)
	}
	waited := false
	defer func() {
		if !waited {
			// A caller's report callback can panic too; never strand a child.
			cancel()
			_ = cmd.Wait()
		}
	}()
	decoder := json.NewDecoder(io.LimitReader(stdout, maxProbeOutput))
	seen := make([]bool, count)
	received := 0
	var protocolErr error
	for {
		var r probeResult
		err := decoder.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			protocolErr = fmt.Errorf("decode probe result: %w", err)
			break
		}
		if r.Index < 0 || r.Index >= count || seen[r.Index] {
			protocolErr = errors.New("invalid or duplicate probe result")
			break
		}
		seen[r.Index] = true
		received++
		report(r)
	}
	if protocolErr != nil {
		cancel()
	}
	waitErr := cmd.Wait() // always reap, including failed startup and cancellation
	waited = true
	if err := ctx.Err(); err != nil && protocolErr == nil {
		return err
	}
	if protocolErr != nil {
		return protocolErr
	}
	if waitErr != nil {
		return fmt.Errorf("probe worker: %w", waitErr)
	}
	if received != count {
		return errors.New("probe worker exited before completing results")
	}
	return nil
}

func serveProbe(input io.Reader, output io.Writer) error {
	var req probeRequest
	if err := json.NewDecoder(io.LimitReader(input, maxProbeInput)).Decode(&req); err != nil {
		return err
	}
	if len(req.Probes) > maxProbeBatch {
		return errors.New("probe batch is too large")
	}
	encoder := json.NewEncoder(output)
	if len(req.Probes) == 0 {
		d, err := measureDelayLocal(context.Background(), req.Config, req.Options)
		r := probeResult{Duration: d}
		if err != nil {
			r.Error = err.Error()
		}
		return encoder.Encode(r)
	}
	// Internal tags are unique and encode result indexes, independent of caller IDs.
	probes := make([]Probe, len(req.Probes))
	for i, p := range req.Probes {
		probes[i] = Probe{Tag: strconv.Itoa(i), Outbound: json.RawMessage(p)}
	}
	var sendErr error
	measureEachLocal(context.Background(), probes, req.Options, min(max(req.Workers, 1), 8), func(tag string, d time.Duration, err error) {
		index, _ := strconv.Atoi(tag)
		r := probeResult{Index: index, Duration: d}
		if err != nil {
			r.Error = err.Error()
		}
		if sendErr == nil {
			sendErr = encoder.Encode(r)
		}
	})
	return sendErr
}
