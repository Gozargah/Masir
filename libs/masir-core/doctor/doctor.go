// Package doctor runs read-only health checks on a masir installation and its
// network path, and says what to do about what it finds. It never changes
// anything: every check inspects, and every problem carries a suggested command.
//
// The checks are split in two: portable ones that work the same on every OS, and
// per-platform ones that only the OS in question can answer. Checks that do not
// apply report Skip with a reason instead of guessing.
package doctor

import (
	"context"
	"fmt"
	"time"
)

// State is how a check came out.
type State int

const (
	OK State = iota
	Warn
	Fail
	Skip
)

func (s State) String() string {
	switch s {
	case OK:
		return "OK"
	case Warn:
		return "WARN"
	case Fail:
		return "FAIL"
	default:
		return "SKIP"
	}
}

// Result is one check's outcome.
type Result struct {
	State  State
	Detail string // what was found
	Fix    string // what to do about it, when something is wrong
}

// Check is a single health test.
type Check struct {
	ID    string
	Title string
	Group string // "Connection", "Network", "System"; the CLI groups by it
	Run   func(ctx context.Context, env Env) Result
}

// Env is the state the CLI already knows. Checks never talk to the daemon
// themselves: the caller passes what it saw, so the checks stay testable.
type Env struct {
	CLIVersion    string
	DaemonRunning bool
	DaemonVersion string
	XrayVersion   string
	XrayRunning   bool
	TunRunning    bool
	Power         bool // masir is connected (not idle)
	StatusError   string
	LastError     string
	ProxyAddress  string
	TunGateway    string
	LatestVersion string
}

// Report pairs a check with its result.
type Report struct {
	ID    string
	Title string
	Group string
	Result
}

// Checks returns the checks for this platform: the portable ones first, then
// the ones only a specific OS can answer.
func Checks() []Check {
	return append(baseChecks(), platformChecks()...)
}

// checkTimeout bounds the whole run; a var so tests can shorten it.
var checkTimeout = 3 * time.Second

// registry is Checks; tests replace it to inject failing checks.
var registry = Checks

// Run executes every check concurrently and returns reports in registration
// order. The whole run is bounded by checkTimeout: a check that ignores its
// context or blocks in a syscall cannot hang the command, it is reported as
// having run out of time instead.
func Run(ctx context.Context, env Env) []Report {
	checks := registry()
	reports := make([]Report, len(checks))
	type outcome struct {
		index  int
		result Result
	}
	done := make(chan outcome, len(checks))
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	for i, c := range checks {
		go func(i int, c Check) {
			done <- outcome{i, safeRun(cctx, c, env)}
		}(i, c)
	}

	seen := make([]bool, len(checks))
	for range checks {
		select {
		case o := <-done:
			seen[o.index] = true
			reports[o.index] = Report{ID: checks[o.index].ID, Title: checks[o.index].Title, Group: checks[o.index].Group, Result: o.result}
		case <-cctx.Done():
			// Whatever has not reported by now is not coming back: fill the rest
			// in and return instead of waiting on it.
			for i, c := range checks {
				if seen[i] {
					continue
				}
				reports[i] = Report{ID: c.ID, Title: c.Title, Group: c.Group, Result: Result{
					State:  Fail,
					Detail: fmt.Sprintf("This check did not finish within %s.", checkTimeout),
					Fix:    "Please report it with `masir version` output.",
				}}
			}
			return reports
		}
	}
	return reports
}

// safeRun turns a panicking check into a failed result instead of a crashed
// command.
func safeRun(ctx context.Context, c Check, env Env) (result Result) {
	defer func() {
		if r := recover(); r != nil {
			result = Result{
				State:  Fail,
				Detail: fmt.Sprintf("This check crashed: %v.", r),
				Fix:    "Please report it with `masir version` output.",
			}
		}
	}()
	return c.Run(ctx, env)
}

// Worst is the severity a summary should report: Fail beats Warn beats OK.
// Skip never counts.
func Worst(reports []Report) State {
	worst := OK
	for _, r := range reports {
		switch r.State {
		case Fail:
			return Fail
		case Warn:
			worst = Warn
		}
	}
	return worst
}
