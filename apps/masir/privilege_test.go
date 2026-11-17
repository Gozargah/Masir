//go:build !windows

package main

import (
	"errors"
	"testing"
)

// A root client can never reach the daemon it starts: the daemon serves SUDO_UID while
// the client looks for root's socket. It is refused with the command to run instead.
func TestRootClientIsRefused(t *testing.T) {
	old := elevated
	t.Cleanup(func() { elevated = old })

	elevated = func() bool { return false }
	if err := rootRefusal("masir connect"); err != nil {
		t.Fatalf("a normal user was refused: %v", err)
	}

	elevated = func() bool { return true }
	err := rootRefusal("masir connect")
	if err == nil {
		t.Fatal("a root client ran connect")
	}
	var ue *userError
	if !errors.As(err, &ue) {
		t.Fatalf("refusal = %T, want the usual failure format", err)
	}
	if len(ue.advice.cmds) != 1 || ue.advice.cmds[0] != "masir connect" {
		t.Fatalf("advice = %v, want the same command without sudo", ue.advice.cmds)
	}
}

// The internal commands are what a privileged masir exists to run: the daemon itself,
// the update helpers, and installing, removing, restarting or restoring the service.
func TestRootMayRunTheDaemonAndServiceCommands(t *testing.T) {
	old := elevated
	t.Cleanup(func() { elevated = old })
	elevated = func() bool { return true }

	for _, path := range []string{
		"masir daemon run",
		"masir daemon sync-service",
		"masir daemon sync-self",
		"masir daemon import-desktop",
		"masir daemon unit",
		"masir daemon sync-unit",
		"masir daemon check",
		"masir daemon enable",
		"masir daemon disable",
		"masir daemon rollback",
		"masir daemon restart",
		"masir desktop install-package",
	} {
		if err := rootRefusal(path); err != nil {
			t.Errorf("%s was refused: %v", path, err)
		}
	}
	for _, path := range []string{"masir", "masir daemon start", "masir daemon stop", "masir update", "masir use", "masir desktop fetch-package"} {
		if err := rootRefusal(path); err == nil {
			t.Errorf("%s was allowed as root", path)
		}
	}
}

// Restarting a session daemon as root is the same mistake as connecting: the restart
// leaves a daemon the root client can't reach. With a service installed, daemonRestart
// asks the service manager before it gets here.
func TestRootCannotRestartASessionDaemon(t *testing.T) {
	old := elevated
	t.Cleanup(func() { elevated = old })

	elevated = func() bool { return false }
	if err := rootRestartRefusal(); err != nil {
		t.Fatalf("a normal user was refused: %v", err)
	}

	elevated = func() bool { return true }
	if err := rootRestartRefusal(); err == nil {
		t.Fatal("a root client restarted a session daemon")
	}
}
