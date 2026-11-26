//go:build !linux

package doctor

// platformChecks is empty off Linux for now: macOS and Windows get their own
// checks (service manager, tunnel adapter, routes, DNS) in a follow-up. Every
// portable check still runs there.
func platformChecks() []Check {
	return nil
}
