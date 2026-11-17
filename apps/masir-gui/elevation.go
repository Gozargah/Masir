package main

import "errors"

// elevationKind tells why a request for root ended without the helper's success, so the
// app can stay quiet after a dismissed dialog and explain the other outcomes.
type elevationKind int

const (
	elevationFailed      elevationKind = iota // root was granted; the helper failed with code
	elevationCancelled                        // the person dismissed the password dialog
	elevationDenied                           // the password was refused or root isn't allowed
	elevationUnavailable                      // no pkexec, or no authentication agent to ask
)

type elevationError struct {
	kind elevationKind
	code int // the helper's exit status for elevationFailed
	msg  string
}

func (e *elevationError) Error() string { return e.msg }

func elevationKindOf(err error) (elevationKind, int, bool) {
	var e *elevationError
	if !errors.As(err, &e) {
		return 0, 0, false
	}
	return e.kind, e.code, true
}
