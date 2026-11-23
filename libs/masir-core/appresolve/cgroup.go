package appresolve

import (
	"os"
	"path/filepath"
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
	ID   string // flatpak app id, or the snap name
}

// Key is the scope as a map key; empty when the process belongs to no app.
func (s Scope) Key() string {
	if s.Kind == "" || s.ID == "" {
		return ""
	}
	return s.Kind + ":" + s.ID
}

// ScopeOf reads the identity out of a /proc/<pid>/cgroup content.
func ScopeOf(cgroup string) Scope {
	// The file ends with a newline; it would keep ".scope" from matching.
	cgroup = strings.TrimSpace(cgroup)
	for _, segment := range strings.Split(cgroup, "/") {
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
		if rest, ok := strings.CutPrefix(segment, "snap."); ok {
			rest = strings.TrimSuffix(rest, ".scope")
			if name, _, found := strings.Cut(rest, "."); found && name != "" {
				return Scope{Kind: "snap", ID: name}
			}
		}
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
// count — /app/... for Flatpak, /snap/... for Snap — so the sandbox's own tools
// (bwrap, xdg-dbus-proxy, snap-confine) never appear.
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
		name := strings.TrimSuffix(filepath.Base(exe), " (deleted)")
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

func insideSandbox(exe string) bool {
	return strings.HasPrefix(exe, "/app/") || strings.HasPrefix(exe, "/snap/")
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

func sharedInterpreter(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "python") {
		return true
	}
	switch name {
	case "node", "electron", "java", "sh", "bash", "dash", "zsh", "perl", "ruby", "php", "env":
		return true
	}
	return false
}
