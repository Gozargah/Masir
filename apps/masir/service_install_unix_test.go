//go:build !windows

package main

import (
	"errors"
	"slices"
	"testing"
)

// A desktop watcher can bring the daemon back between the user's stop and sudo.
// Exercise both invocations without installing anything on the host.
func TestInstallConfirmsOnceAcrossElevation(t *testing.T) {
	oldElevated, oldElevate := elevated, elevate
	oldSocket, oldStop, oldConfirm := liveDaemonSocket, stopTheDaemon, confirmInstallStop
	oldPreflight := preflightGeoForInstall
	t.Cleanup(func() {
		elevated, elevate = oldElevated, oldElevate
		liveDaemonSocket, stopTheDaemon, confirmInstallStop = oldSocket, oldStop, oldConfirm
		preflightGeoForInstall = oldPreflight
	})
	preflightGeoForInstall = func(string) error { return nil }

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

func TestInstallPreflightRunsBeforeStoppingDaemon(t *testing.T) {
	oldElevated, oldElevate := elevated, elevate
	oldSocket, oldStop, oldConfirm := liveDaemonSocket, stopTheDaemon, confirmInstallStop
	oldPreflight := preflightGeoForInstall
	t.Cleanup(func() {
		elevated, elevate = oldElevated, oldElevate
		liveDaemonSocket, stopTheDaemon, confirmInstallStop = oldSocket, oldStop, oldConfirm
		preflightGeoForInstall = oldPreflight
	})

	elevated = func() bool { return false }
	liveDaemonSocket = func() (string, bool) { return "/tmp/daemon.sock", true }
	stops, prompts := 0, 0
	confirmInstallStop = func(string, bool) (bool, error) {
		prompts++
		return true, nil
	}
	stopTheDaemon = func(string) error {
		stops++
		return nil
	}
	elevate = func(...string) error {
		t.Fatal("elevate should not run when preflight fails")
		return nil
	}
	wantErr := errors.New("missing geo data")
	preflightGeoForInstall = func(string) error { return wantErr }

	if err := daemonEnable(args{}); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if prompts != 0 || stops != 0 {
		t.Fatalf("prompts=%d stops=%d, want 0 when preflight fails", prompts, stops)
	}
}

func TestInstallDoesNotForwardConfirmedWhenNoDaemonWasRunning(t *testing.T) {
	oldElevated, oldElevate := elevated, elevate
	oldSocket, oldStop, oldConfirm := liveDaemonSocket, stopTheDaemon, confirmInstallStop
	oldPreflight := preflightGeoForInstall
	t.Cleanup(func() {
		elevated, elevate = oldElevated, oldElevate
		liveDaemonSocket, stopTheDaemon, confirmInstallStop = oldSocket, oldStop, oldConfirm
		preflightGeoForInstall = oldPreflight
	})

	preflightGeoForInstall = func(string) error { return nil }
	isRoot := false
	elevated = func() bool { return isRoot }
	// Nothing running before elevation; a daemon starts while elevating.
	liveDaemonSocket = func() (string, bool) {
		if !isRoot {
			return "", false
		}
		return "/tmp/daemon.sock", true
	}
	prompts := 0
	confirmInstallStop = func(string, bool) (bool, error) {
		prompts++
		return false, nil
	}
	stopTheDaemon = func(string) error {
		t.Fatal("should not stop without confirmation")
		return nil
	}
	var forwarded []string
	elevate = func(argv ...string) error {
		forwarded = append([]string(nil), argv...)
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

	err := daemonEnable(args{})
	if err == nil {
		t.Fatal("expected refusal when user declines stopping daemon that started during elevation")
	}
	if slices.Contains(forwarded, "--confirmed") {
		t.Fatalf("elevated argv = %v, must not include --confirmed when no daemon was running", forwarded)
	}
	if prompts != 1 {
		t.Fatalf("prompts = %d, want 1 in elevated check", prompts)
	}
}
