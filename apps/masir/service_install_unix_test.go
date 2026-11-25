//go:build !windows

package main

import (
	"errors"
	"testing"
)

// A desktop watcher can bring the daemon back between the user's stop and sudo.
// Exercise both invocations without installing anything on the host.
func TestInstallConfirmsOnceAcrossElevation(t *testing.T) {
	oldElevated, oldElevate := elevated, elevate
	oldSocket, oldStop, oldConfirm := liveDaemonSocket, stopTheDaemon, confirmInstallStop
	t.Cleanup(func() {
		elevated, elevate = oldElevated, oldElevate
		liveDaemonSocket, stopTheDaemon, confirmInstallStop = oldSocket, oldStop, oldConfirm
	})

	isRoot := false
	elevated = func() bool { return isRoot }
	liveDaemonSocket = func() (string, bool) { return "/tmp/daemon.sock", true }
	prompts, stops := 0, 0
	confirmInstallStop = func(string, bool) (bool, error) {
		prompts++
		return true, nil
	}
	stopTheDaemon = func(string) error {
		stops++
		if isRoot {
			return errors.New("stop here before touching the host service")
		}
		return nil
	}
	elevate = func(argv ...string) error {
		isRoot = true
		cmd, rest, err := rootCmd().Find(argv)
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Fatal(err)
		}
		return daemonEnable(argsFrom(cmd, cmd.Flags().Args()))
	}
	if err := daemonEnable(args{}); !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want the stop failure", err)
	}
	if prompts != 1 {
		t.Fatalf("asked to stop and install %d times, want 1", prompts)
	}
	if stops != 2 {
		t.Fatalf("stops = %d, want both the original and restarted daemon stopped", stops)
	}
}
