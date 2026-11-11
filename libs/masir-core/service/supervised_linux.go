//go:build linux

package service

import "os"

// Supervised reports whether the service manager started this process. systemd puts an
// invocation id in the environment of every unit it starts; the daemon a CLI spawns with
// sudo has none, which is what tells a service restart from a stray session daemon.
func Supervised() bool { return os.Getenv("INVOCATION_ID") != "" }
