package xray

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const (
	probeWorkerArg        = "--masir-internal-xray-probe-v1"
	maxProbeInput         = 16 << 20
	maxProbeOutput        = 1 << 20
	probeLifetime         = 2 * time.Minute
	probeStartupAllowance = 10 * time.Second
)

// Each helper owns one config and its Xray global state. Bound helpers across
// all callers, including individual tests and simultaneous subscription tests.
const maxProbeWorkers = 8

var probeSlot = make(chan struct{}, maxProbeWorkers)

const probeTunMissingExit = 3

var errProbeTunMissing = errors.New("probe TUN interface disappeared")

type probeRequest struct {
	Config  []byte
	Options LatencyOptions
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
		// Results own stdout. Xray prints to it on its own: a deprecation warning while
		// the config parses (before the probe's quiet log config exists), REALITY's
		// `show` traces, congestion debug output. Any of those ahead of a result made
		// the parent read a warning as JSON. Send every other writer to stderr, which
		// the parent discards, and keep the real stdout for results alone.
		results := os.Stdout
		os.Stdout = os.Stderr
		// Bound orphan lifetime too, even if the parent exits or core.New hangs.
		time.AfterFunc(probeLifetime, func() { os.Exit(1) })
		if err := serveProbe(os.Stdin, results); err != nil {
			if errors.Is(err, errProbeTunMissing) {
				os.Exit(probeTunMissingExit)
			}
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

// Probe is either a single outbound or a complete, composed routing profile.
// Profiles keep their dependencies, DNS, routing and observatory in their own helper.
type Probe struct {
	Tag      string
	Outbound json.RawMessage
	Profile  []byte
}

// MeasureEach uses a sliding worker pool: a finished config immediately makes room
// for the next one, even while earlier configs are slow. Reports are serialized.
func MeasureEach(ctx context.Context, probes []Probe, opt LatencyOptions, workers int, report func(string, time.Duration, error)) {
	var reports sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan Probe)
	for range min(max(workers, 1), maxProbeWorkers, len(probes)) {
		wg.Go(func() {
			for p := range jobs {
				options := opt
				raw := p.Profile
				var err error
				if raw != nil {
					options.Full = true
					options.BalancedProfile = true
				} else {
					raw, err = json.Marshal(map[string]any{"outbounds": []json.RawMessage{p.Outbound}})
				}
				var d time.Duration
				if err == nil {
					d, err = MeasureDelay(ctx, raw, options)
				}
				reports.Lock()
				report(p.Tag, d, err)
				reports.Unlock()
			}
		})
	}
	for _, p := range probes {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
}

// runProbe owns the complete helper lifecycle; no caller can fall back to an
// in-process instance. Results are bounded and validated before reaching callers.
func runProbe(ctx context.Context, req probeRequest, count int, report func(probeResult)) error {
	req.Options = req.Options.withDefaults()
	if req.Options.Timeout > (probeLifetime-probeStartupAllowance)/time.Duration(req.Options.Attempts) {
		return errors.New("probe request budget exceeds helper lifetime")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case probeSlot <- struct{}{}:
	}
	defer func() { <-probeSlot }()
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, probeLifetime)
	defer cancel()
	currentTunName := req.Options.CurrentTunName
	if currentTunName != nil {
		req.Options.TunName = currentTunName()
	}
	err := runProbeWorker(ctx, req, count, report)
	if errors.Is(err, errProbeTunMissing) && currentTunName != nil {
		if name := currentTunName(); name != req.Options.TunName {
			req.Options.TunName = name
			return runProbeWorker(ctx, req, count, report)
		}
	}
	return err
}

func runProbeWorker(ctx context.Context, req probeRequest, count int, report func(probeResult)) error {
	ctx, cancel := context.WithCancel(ctx)
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
	results := bufio.NewReader(io.LimitReader(stdout, maxProbeOutput))
	received, protocolErr := readProbeResults(results, count, report)
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
		var exit *exec.ExitError
		if received == 0 && errors.As(waitErr, &exit) && exit.ExitCode() == probeTunMissingExit {
			return errProbeTunMissing
		}
		return fmt.Errorf("probe worker: %w", waitErr)
	}
	if received != count {
		return errors.New("probe worker exited before completing results")
	}
	return nil
}

// readProbeResults decodes one JSON object per line until the stream ends. Every result
// is one line of our own encoding, so anything else on stdout belongs to Xray: a
// deprecation warning logged while the config parses (before the probe's quiet log
// config exists), REALITY's `show` trace, or congestion debug output. Those lines are
// skipped, not treated as an error, so noise never costs a whole batch its verdict.
func readProbeResults(results *bufio.Reader, count int, report func(probeResult)) (int, error) {
	seen := make([]bool, count)
	received := 0
	for {
		line, readErr := results.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 && line[0] == '{' {
			var r probeResult
			if err := json.Unmarshal(line, &r); err != nil {
				return received, fmt.Errorf("decode probe result: %w", err)
			}
			if r.Index < 0 || r.Index >= count || seen[r.Index] {
				return received, errors.New("invalid or duplicate probe result")
			}
			seen[r.Index] = true
			received++
			report(r)
		}
		if readErr == io.EOF {
			return received, nil
		}
		if readErr != nil {
			return received, fmt.Errorf("read probe result: %w", readErr)
		}
	}
}

func serveProbe(input io.Reader, output io.Writer) error {
	var req probeRequest
	if err := json.NewDecoder(io.LimitReader(input, maxProbeInput)).Decode(&req); err != nil {
		return err
	}
	if err := configureProbeInterface(req.Options.TunName); err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	d, err := measureDelayLocal(context.Background(), req.Config, req.Options)
	r := probeResult{Duration: d}
	if err != nil {
		r.Error = err.Error()
	}
	return encoder.Encode(r)
}
