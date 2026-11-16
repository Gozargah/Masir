package analytics

import (
	"errors"
	"regexp"
	"runtime"
	"strings"
	"time"

	coreanalytics "github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/posthog/posthog-go"
)

// exceptionFrames contains only Masir code locations, never absolute paths,
// arguments, raw debug.Stack output, machine addresses, or runtime locals.
func exceptionFrames() []posthog.StackFrame {
	var pcs [48]uintptr
	n := runtime.Callers(3, pcs[:])
	it := runtime.CallersFrames(pcs[:n])
	var frames []posthog.StackFrame
	for {
		f, more := it.Next()
		const prefix = "github.com/Gozargah/Masir/"
		if strings.HasPrefix(f.Function, prefix) && !strings.Contains(f.Function, "internal/analytics.") {
			file := strings.ReplaceAll(f.File, "\\", "/")
			if i := strings.Index(file, "/libs/"); i >= 0 {
				file = file[i+1:]
			} else if i := strings.Index(file, "/apps/"); i >= 0 {
				file = file[i+1:]
			} else {
				file = ""
			}
			frames = append(frames, posthog.StackFrame{Filename: file, Function: strings.TrimPrefix(f.Function, prefix), LineNo: f.Line, InApp: true, Platform: "go"})
		}
		if !more {
			break
		}
	}
	// PostHog orders frames oldest first.
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
	return frames
}
func (c *Client) trackException(event string, err error, props map[string]any) {
	c.trackExceptionAt(event, err, props, time.Now().UTC())
}
func (c *Client) trackExceptionAt(event string, err error, props map[string]any, ts time.Time) {
	c.sync()
	c.mu.Lock()
	api, enabled, id := c.api, c.enabled, c.installID
	c.mu.Unlock()
	if !enabled || api == nil {
		return
	}
	p := posthog.NewProperties()
	for k, v := range coreanalytics.Properties(props) {
		p.Set(k, v)
	}
	p.Set("$process_person_profile", false)
	p.Set("schema_version", coreanalytics.SchemaVersion)
	p.Set("component", "daemon")
	p.Set("app_version", c.version)
	p.Set("process_run_id", c.runID)
	p.Set("origin_event", event)
	handled := !strings.Contains(event, "panic")
	frames := exceptionFrames()
	if source, _ := props["source"].(string); strings.HasSuffix(source, "_spool") || strings.HasSuffix(source, "_report") {
		frames = nil
	}
	stack := &posthog.ExceptionStacktrace{Type: "raw", Frames: frames}
	fingerprint := exceptionFingerprint(event, p, stack)
	uuid := coreanalytics.EventID()
	if id, _ := props["event_id"].(string); validEventID(id) {
		// Separate deterministic UUID for the native exception accompanying a client event.
		const hex = "0123456789abcdef"
		digit := strings.IndexByte(hex, id[len(id)-1])
		uuid = id[:len(id)-1] + string(hex[digit^1])
	}
	c.enqueue(posthog.Exception{Uuid: uuid, DistinctId: id, Timestamp: ts, Properties: p,
		ExceptionFingerprint: &fingerprint,
		ExceptionList:        []posthog.ExceptionItem{{Type: event, Value: coreanalytics.SanitizeMessage(err.Error()), Mechanism: &posthog.ExceptionMechanism{Handled: &handled}, Stacktrace: stack}},
	})
}

var safeCodeLocation = regexp.MustCompile(`^[A-Za-z0-9_./*()\[\]-]+$`)

func cleanStack(stack *posthog.ExceptionStacktrace) *posthog.ExceptionStacktrace {
	if stack == nil {
		return nil
	}
	clean := &posthog.ExceptionStacktrace{Type: "raw"}
	for _, f := range stack.Frames {
		if len(clean.Frames) >= 32 {
			break
		}
		if (!strings.HasPrefix(f.Filename, "libs/") && !strings.HasPrefix(f.Filename, "apps/")) || strings.Contains(f.Filename, "..") || len(f.Filename) > 240 || len(f.Function) > 240 || !safeCodeLocation.MatchString(f.Filename) || !safeCodeLocation.MatchString(f.Function) || f.LineNo <= 0 || f.LineNo > 1000000 {
			continue
		}
		clean.Frames = append(clean.Frames, posthog.StackFrame{Filename: f.Filename, Function: f.Function, LineNo: f.LineNo, InApp: true, Platform: "go"})
	}
	return clean
}
func exceptionFingerprint(event string, p posthog.Properties, stack *posthog.ExceptionStacktrace) string {
	kind, _ := p["error_kind"].(string)
	fingerprint := event + ":" + kind
	if hash, _ := p["stack_fingerprint"].(string); len(hash) == 12 && strings.Trim(hash, "0123456789abcdef") == "" {
		fingerprint += ":" + hash
	}
	if stack != nil && len(stack.Frames) > 0 {
		fingerprint += ":" + stack.Frames[len(stack.Frames)-1].Function
	}
	return fingerprint
}

// TrackClientException records a client-reported panic without inventing a
// daemon stack for a failure that occurred in another process.
func (c *Client) TrackClientException(event string, props map[string]any, ts time.Time) {
	if event != "cli_panic" && event != "gui_panic" {
		return
	}
	clean := coreanalytics.Properties(props)
	clean["source"] = "cli_report"
	if event == "gui_panic" {
		clean["source"] = "gui_report"
	}
	message, _ := clean["message"].(string)
	c.trackExceptionAt(event, errors.New(message), clean, ts)
}
