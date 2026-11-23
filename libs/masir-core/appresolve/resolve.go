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
		if len(tokens) > 1 && tokens[1] == "run" {
			if v := flatpakCommand(tokens[2:]); v != "" {
				return v, nil
			}
		}
		return "", ErrUnresolvable
	case "snap":
		// snap run NAME [args]
		if len(tokens) > 2 && tokens[1] == "run" {
			return tokens[2], nil
		}
		return "", ErrUnresolvable
	case "env":
		return resolveEnv(tokens[1:], depth)
	case "sh", "bash", "dash", "zsh", "gtk-launch":
		// Command strings and desktop-id indirection hide the real program.
		return "", ErrUnresolvable
	}
	// /snap/bin/NAME — and newer snapd's /var/lib/snapd/snap/bin/NAME — symlink
	// to the snap launcher: the program is NAME, not the snap binary it links to.
	if isSnapBinPath(name) {
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

// isSnapBinPath reports the two layouts snapd uses for its command symlinks.
func isSnapBinPath(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.Contains(p, "/snap/bin/") || strings.Contains(p, "/snapd/snap/bin/")
}

// flatpakCommand reads run's own options up to the application reference. Only
// an option before the app id belongs to the launcher — after it, arguments are
// passed to the app, so `flatpak run APP --command=worker` is not a command
// override.
func flatpakCommand(args []string) string {
	command := ""
	for i := 0; i < len(args); i++ {
		t := args[i]
		if !strings.HasPrefix(t, "-") {
			break // the app reference: launcher options end here
		}
		if v, ok := strings.CutPrefix(t, "--command="); ok {
			command = v
			continue
		}
		if t == "--command" && i+1 < len(args) {
			command = args[i+1]
			i++
			continue
		}
		if flatpakValueOptions[t] && i+1 < len(args) {
			i++ // the option's value is not the app id
		}
	}
	return command
}

var flatpakValueOptions = map[string]bool{
	"--arch": true, "--branch": true, "--runtime": true, "--app-path": true,
	"--usr-path": true, "--sdk-path": true, "--filesystem": true, "--env": true,
}

// resolveEnv walks env's own options and assignments down to the command it
// runs. Options with operands consume them; an unknown option is unresolvable
// because its operand is unknowable.
func resolveEnv(rest []string, depth int) (string, error) {
	pathOverride := false
	for len(rest) > 0 {
		t := rest[0]
		switch {
		case envOperandOptions[t]:
			if len(rest) < 2 {
				return "", ErrUnresolvable
			}
			rest = rest[2:]
		case strings.HasPrefix(t, "--unset="), strings.HasPrefix(t, "--chdir="):
			rest = rest[1:]
		case envFlagOptions[t]:
			rest = rest[1:]
		case strings.HasPrefix(t, "-"):
			return "", ErrUnresolvable // unknown option: its operand is unknowable
		case strings.Contains(t, "="):
			if strings.HasPrefix(t, "PATH=") {
				pathOverride = true
			}
			rest = rest[1:]
		default:
			// A relative command under an overridden PATH would be looked up in
			// the wrong path; launchers below do not use PATH, so they are safe.
			if pathOverride && !strings.Contains(t, "/") && !launcherNames[filepath.Base(t)] {
				return "", ErrUnresolvable
			}
			return resolveTokens(rest, depth+1)
		}
	}
	return "", ErrUnresolvable
}

var envOperandOptions = map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true}
var envFlagOptions = map[string]bool{"-i": true, "--ignore-environment": true, "-0": true, "--null": true, "-v": true, "--debug": true}
var launcherNames = map[string]bool{"flatpak": true, "snap": true, "env": true, "sh": true, "bash": true, "dash": true, "zsh": true, "gtk-launch": true}

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
