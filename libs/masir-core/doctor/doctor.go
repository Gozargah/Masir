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
	"sync"
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

const checkTimeout = 3 * time.Second

// registry is Checks; tests replace it to inject failing checks.
var registry = Checks

// Run executes every check concurrently and returns reports in registration
// order. A check that panics or overruns its budget fails alone.
func Run(ctx context.Context, env Env) []Report {
	checks := registry()
	reports := make([]Report, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					reports[i] = Report{ID: c.ID, Title: c.Title, Group: c.Group, Result: Result{
						State:  Fail,
						Detail: fmt.Sprintf("This check crashed: %v.", r),
						Fix:    "Please report it with `masir version` output.",
					}}
				}
			}()
			cctx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			reports[i] = Report{ID: c.ID, Title: c.Title, Group: c.Group, Result: c.Run(cctx, env)}
		}(i, c)
	}
	wg.Wait()
	return reports
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
