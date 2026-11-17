package main

import (
	"errors"
	"testing"

	"github.com/Gozargah/Masir/apps/masir/internal/ui"
)

// JSON mode runs fastest with no spinner (quiet): a total probe failure must
// come back as a normal error so report() prints why the command exited 1 —
// not as errReported, which exits silently because a spinner was supposed to
// have shown the message.
func TestFastestFailureQuietReportsReason(t *testing.T) {
	err := fastestFailure(nil)
	if err == nil {
		t.Fatal("quiet failure returned nil error")
	}
	if errors.Is(err, errReported) {
		t.Error("quiet failure must not be errReported: nothing reported it")
	}
	if err.Error() != "No proxy responded." {
		t.Errorf("message = %q, want %q", err.Error(), "No proxy responded.")
	}
}

// The spinner path keeps its contract: the spinner shows the message and the
// error stays errReported so report() does not print it twice.
func TestFastestFailureSpinnerReports(t *testing.T) {
	err := fastestFailure(&ui.Spinner{})
	if !errors.Is(err, errReported) {
		t.Errorf("error = %v, want errReported", err)
	}
}
