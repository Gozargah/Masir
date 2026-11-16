package update

// diagnosticError preserves a bounded reason and status independently of error
// text. The message remains unchanged for the CLI and the local log.
type diagnosticError struct {
	err    error
	reason string
	status int
}

func (e *diagnosticError) Error() string             { return e.err.Error() }
func (e *diagnosticError) Unwrap() error             { return e.err }
func (e *diagnosticError) DiagnosticReason() string  { return e.reason }
func (e *diagnosticError) DiagnosticHTTPStatus() int { return e.status }
