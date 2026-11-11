package client

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// StartError is why starting a background daemon failed. It keeps what a person needs
// to tell the cases apart: where this command looked, where a daemon actually answers,
// and what the daemon itself wrote before it stopped.
type StartError struct {
	Socket  string        // where this command started its daemon
	Live    string        // a daemon that does answer, when it isn't on Socket
	LogPath string        // the file the daemon's output goes to
	Exited  bool          // the child stopped before the socket appeared
	Exit    error         // how it stopped, when that is a failure
	Timeout time.Duration // how long the client waited, when it timed out
	Tail    []string      // the last lines the daemon wrote
}

func (e *StartError) Error() string {
	switch {
	case e.Exited && e.Exit != nil:
		return fmt.Sprintf("the masir daemon exited before it started: %v", e.Exit)
	case e.Exited:
		return "the masir daemon stopped before it started"
	case e.Live != "":
		return fmt.Sprintf("the masir daemon didn't start on %s; a daemon answers on %s", e.Socket, e.Live)
	default:
		return fmt.Sprintf("the masir daemon didn't start on %s within %s", e.Socket, e.Timeout)
	}
}

// Detail is what the daemon wrote, one line each, for a failure report with room for it.
// It is empty when the log had nothing to say: a test, or a daemon that died before it
// opened its mouth.
func (e *StartError) Detail() string { return strings.Join(e.Tail, "\n") }

// tailBytes bounds how much of the log a failure report carries.
const tailBytes = 8 << 10

// startTail returns the last n non-empty lines of a log, oldest first. It never fails:
// a missing or unreadable log is no reason to lose the failure that matters.
func startTail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	offset := fi.Size() - tailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, tailBytes))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	tail := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(tail) < n; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			tail = append([]string{line}, tail...)
		}
	}
	return tail
}
