package main

// Exit codes the desktop app reads from `masir desktop ...`; 1 is any other failure.
const (
	exitMismatch               = 10 // the package isn't the signed release
	exitInstallerBusy          = 11 // the system package manager is in use
	exitInstallFailed          = 12 // the package manager refused the package
	exitServicePending         = 13 // the installed service hasn't taken the new version
	exitNotNewer               = 14 // the package isn't newer than what is installed
	exitAuthorizationCancelled = 15 // the administrator dismissed the prompt
	exitUnitPending            = 16 // the installed service unit needs reconciliation
	exitUnreachable            = 20 // the release host couldn't be reached
)

// codedError is an error that picks the process's exit status.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func coded(code int, err error) error { return &codedError{code, err} }
