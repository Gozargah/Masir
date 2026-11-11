//go:build darwin

package service

import "os"

// Supervised reports whether launchd started this process. launchd sets the job's label
// in XPC_SERVICE_NAME; the daemon the app starts through the password prompt has none.
func Supervised() bool { return os.Getenv("XPC_SERVICE_NAME") != "" }
