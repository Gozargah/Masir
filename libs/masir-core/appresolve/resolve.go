// Package appresolve turns a desktop launcher's Exec line into the value a
// process rule must carry. Process rules match a program by its name, its exact
// path, or a folder prefix, so a launcher — which is not the program — has to be
// decoded to the program it starts. Anything that cannot be named reliably is
// reported as unresolvable instead of guessed at: a wrong rule matches silently,
// which is worse than no entry at all.
package appresolve

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrUnresolvable reports a launcher whose real program cannot be named from its
// Exec line: a shell command string, a launcher without a target, a field code.
var ErrUnresolvable = errors.New("appresolve: no single program to match")

// Resolve returns the routing value for a desktop Exec line:
//
//   - a bare process name ("spotify") — matched against the basename of the
//     process path, so it works for sandboxed apps whose install location
//     differs per machine;
//   - an absolute path ("/app/opt/stremio/stremio") — matched exactly; flatpak
//     processes are seen at their sandbox paths, which are the same everywhere;
//   - the resolved executable of a plain entry ("/usr/bin/spotify").
func Resolve(declare string) (string, error) {
	tokens := Tokenize(declare)
	if len(tokens) == 0 {
		return "", ErrUnresolvable
	}
	return resolveTokens(tokens, 0)
}

// maxLauncherDepth stops env-of-env-of-env chains from recursing forever.
const maxLauncherDepth = 4

func resolveTokens(tokens []string, depth int) (string, error) {
	if depth > maxLauncherDepth {
		return "", ErrUnresolvable
	}
	name := tokens[0]
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	switch base {
	case "flatpak":
		// flatpak run [opts] --command=CMD [opts] APP-ID [args]
		// The sandboxed program is the --command target; without one the entry
		// gives no reliable name.
		if v := flagValue(tokens, "--command"); v != "" {
			return v, nil
		}
		return "", ErrUnresolvable
	case "snap":
		// snap run NAME [args]
		if len(tokens) > 2 && tokens[1] == "run" {
			return tokens[2], nil
		}
		return "", ErrUnresolvable
	case "env":
		// env [-i] [VAR=value …] CMD [args] — the program follows the assignments.
		for i := 1; i < len(tokens); i++ {
			if strings.HasPrefix(tokens[i], "-") || strings.Contains(tokens[i], "=") {
				continue
			}
			return resolveTokens(tokens[i:], depth+1)
		}
		return "", ErrUnresolvable
	case "sh", "bash", "dash", "zsh", "gtk-launch":
		// Command strings and desktop-id indirection hide the real program.
		return "", ErrUnresolvable
	}
	// /snap/bin/NAME is a symlink to the snap launcher: the program is NAME,
	// not the /usr/bin/snap it links to.
	if strings.HasPrefix(name, "/snap/bin/") {
		return base, nil
	}
	if name == "" || strings.Contains(name, "%") {
		return "", ErrUnresolvable
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", ErrUnresolvable
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	return resolved, nil
}

// flagValue returns the value of --flag=value or --flag value.
func flagValue(tokens []string, flag string) string {
	for i, t := range tokens {
		if v, ok := strings.CutPrefix(t, flag+"="); ok {
			return v
		}
		if t == flag && i+1 < len(tokens) {
			return tokens[i+1]
		}
	}
	return ""
}

// Tokenize splits a desktop Exec line into arguments the way desktop-entry files
// intend: quotes group, a backslash escapes the next character.
func Tokenize(command string) []string {
	var tokens []string
	var token strings.Builder
	quoted, escaped, have := false, false, false
	flush := func() {
		if have {
			tokens = append(tokens, token.String())
			token.Reset()
			have = false
		}
	}
	for _, r := range command {
		if escaped {
			token.WriteRune(r)
			escaped, have = false, true
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			quoted, have = !quoted, true
			continue
		}
		if !quoted && (r == ' ' || r == '\t') {
			flush()
			continue
		}
		token.WriteRune(r)
		have = true
	}
	flush()
	return tokens
}
