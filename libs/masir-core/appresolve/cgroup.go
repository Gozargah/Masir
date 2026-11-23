package appresolve

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Scope is the application a process belongs to, as its cgroup names it.
//
// The sandbox sets that name, so it identifies the app no matter which binary,
// wrapper or interpreter the app runs: Flatpak puts every instance in
// app-flatpak-<app id>-*.scope and snapd puts every app in
// snap.<snap>.<app>-*.scope.
type Scope struct {
	Kind string // "flatpak" or "snap"
	// ID is the flatpak app id, or the snap instance: the snap name, and for a
	// snap exposing several apps <name>.<app> — the form its launcher uses too.
	ID string
}

// Key is the scope as a map key; empty when the process belongs to no app.
func (s Scope) Key() string {
	if s.Kind == "" || s.ID == "" {
		return ""
	}
	return s.Kind + ":" + s.ID
}

// snapUUID strips the trailing -<uuid>.scope of a snap cgroup scope.
var snapUUID = regexp.MustCompile(`-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.scope$`)

// ScopeOf reads the identity out of a /proc/<pid>/cgroup content. The file has
// one record per hierarchy — <id>:<controllers>:<path> — and cgroup-v1 hosts
// carry several records, so each is parsed on its own.
func ScopeOf(cgroup string) Scope {
	for _, line := range strings.Split(strings.TrimSpace(cgroup), "\n") {
		_, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		_, path, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		if scope := scopeInPath(path); scope.Key() != "" {
			return scope
		}
	}
	return Scope{}
}

func scopeInPath(path string) Scope {
	for _, segment := range strings.Split(path, "/") {
		// Only an application scope names an app. On cgroup-v1 hosts snapd also
		// uses a package-wide freezer group (/snap.<instance>, no .scope): it
		// must not end the search before the application scope is read.
		if !strings.HasSuffix(segment, ".scope") {
			continue
		}
		if i := strings.Index(segment, "app-flatpak-"); i >= 0 {
			id := strings.TrimSuffix(segment[i+len("app-flatpak-"):], ".scope")
			// The trailing hyphen-separated number distinguishes instances.
			if j := strings.LastIndex(id, "-"); j > 0 && isDigits(id[j+1:]) {
				id = id[:j]
			}
			if id != "" {
				return Scope{Kind: "flatpak", ID: id}
			}
		}
		rest, ok := strings.CutPrefix(segment, "snap.")
		if !ok {
			continue
		}
		rest = snapUUID.ReplaceAllString(rest, "")
		rest = strings.TrimSuffix(rest, ".scope")
		name, app, found := strings.Cut(rest, ".")
		if name == "" {
			continue
		}
		// One app per snap is the default one, launched as the instance itself;
		// a snap with several apps is addressed as <instance>.<app>. An instance
		// is named <snap>_<key>, so the app "firefox" of "firefox_firefox" is
		// still its default app.
		if !found || app == name || app == strings.SplitN(name, "_", 2)[0] {
			return Scope{Kind: "snap", ID: name}
		}
		return Scope{Kind: "snap", ID: name + "." + app}
	}
	return Scope{}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ObservedApps maps Scope.Key() to the executable names its processes run,
// most-used first. Only processes whose executable lives inside the sandbox
// count — /app/... for Flatpak, the snap mount for Snap — so the sandbox's own
// tools (bwrap, xdg-dbus-proxy, snap-confine) never appear.
func ObservedApps() map[string][]string {
	return observedApps("/proc")
}

func observedApps(procRoot string) map[string][]string {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	counts := map[string]map[string]int{}
	for _, e := range entries {
		pid := e.Name()
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(procRoot, pid, "cgroup"))
		if err != nil {
			continue
		}
		scope := ScopeOf(string(data))
		if scope.Key() == "" {
			continue
		}
		exe, err := os.Readlink(filepath.Join(procRoot, pid, "exe"))
		if err != nil || !insideSandbox(exe) {
			continue
		}
		name := strings.TrimSuffix(exe, " (deleted)")
		name = filepath.Base(name)
		if name == "" {
			continue
		}
		m := counts[scope.Key()]
		if m == nil {
			m = map[string]int{}
			counts[scope.Key()] = m
		}
		m[name]++
	}
	out := make(map[string][]string, len(counts))
	for key, m := range counts {
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool {
			if m[names[i]] != m[names[j]] {
				return m[names[i]] > m[names[j]]
			}
			return names[i] < names[j]
		})
		out[key] = names
	}
	return out
}

// insideSandbox reports whether an executable path belongs to a sandboxed app.
// The snap mount directory is configurable at build time, and for
// classic-confined snaps a host-side reader of /proc/<pid>/exe sees the host
// path — the two layouts in practice are /snap/... and /var/lib/snapd/snap/...
func insideSandbox(exe string) bool {
	return strings.HasPrefix(exe, "/app/") ||
		strings.HasPrefix(exe, "/snap/") ||
		strings.Contains(exe, "/snapd/snap/")
}

// BestName picks the executable name a rule should carry: the most-used one
// that is not a shared interpreter. An app that only runs an interpreter (a
// Python app runs python3) gets "", because a rule on python3 would catch every
// Python app on the machine.
func BestName(names []string) string {
	for _, name := range names {
		if name == "" || sharedInterpreter(name) {
			continue
		}
		return name
	}
	return ""
}

// versionSuffix trims the version off an interpreter name: ruby3.3 -> ruby.
var versionSuffix = regexp.MustCompile(`[0-9]+(\.[0-9]+)*$`)

func sharedInterpreter(name string) bool {
	name = versionSuffix.ReplaceAllString(strings.ToLower(name), "")
	switch name {
	case "python", "node", "nodejs", "electron", "java", "sh", "bash", "dash", "zsh",
		"perl", "ruby", "php", "env", "lua", "tclsh", "wish", "busybox":
		return true
	}
	return false
}
