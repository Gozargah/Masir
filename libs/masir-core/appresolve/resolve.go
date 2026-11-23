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

// Resolve returns the process rule value for an Exec line. Use FromDesktop
// when the desktop file is available so Flatpak exports can be inspected.
func Resolve(command string) (string, error) {
	return resolve(command, "")
}

func resolve(command, desktopPath string) (string, error) {
	tokens := Tokenize(command)
	if len(tokens) == 0 {
		return "", ErrUnresolvable
	}
	return resolveTokens(tokens, desktopPath, 0)
}

// maxLauncherDepth stops env-of-env-of-env chains from recursing forever.
const maxLauncherDepth = 4

func resolveTokens(tokens []string, desktopPath string, depth int) (string, error) {
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
			return flatpakExecutable(desktopPath, flatpakCommand(tokens[2:]))
		}
		return "", ErrUnresolvable
	case "snap":
		// snap run NAME [args]
		if len(tokens) > 2 && tokens[1] == "run" {
			return installedSnapExecutable(tokens[2])
		}
		return "", ErrUnresolvable
	case "env":
		return resolveEnv(tokens[1:], desktopPath, depth)
	case "sh", "bash", "dash", "zsh", "gtk-launch":
		// Command strings and desktop-id indirection hide the real program.
		return "", ErrUnresolvable
	}
	// /snap/bin/NAME — and newer snapd's /var/lib/snapd/snap/bin/NAME — symlink
	// to the snap launcher: the program is NAME, not the snap binary it links to.
	if isSnapBinPath(name) {
		return installedSnapExecutable(base)
	}
	if name == "" || strings.Contains(name, "%") {
		return "", ErrUnresolvable
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", ErrUnresolvable
	}
	if isSnapBinPath(resolved) {
		return installedSnapExecutable(filepath.Base(resolved))
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	return resolved, nil
}

// isSnapBinPath reports the two layouts snapd uses for its command symlinks.
func isSnapBinPath(p string) bool {
	dir := filepath.ToSlash(filepath.Dir(filepath.Clean(p)))
	return dir == "/snap/bin" || dir == "/var/lib/snapd/snap/bin"
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
			return command // the app reference: launcher options end here
		}
		if t == "--" {
			if i+1 < len(args) {
				return command
			}
			break
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
	return ""
}

var flatpakValueOptions = map[string]bool{
	"--arch": true, "--branch": true, "--runtime": true, "--app-path": true,
	"--usr-path": true, "--sdk-path": true, "--filesystem": true, "--env": true,
}

// resolveEnv walks env's own options and assignments down to the command it
// runs. Options with operands consume them; an unknown option is unresolvable
// because its operand is unknowable.
func resolveEnv(rest []string, desktopPath string, depth int) (string, error) {
	lookupChanged := false
	for len(rest) > 0 {
		t := rest[0]
		switch {
		case envOperandOptions[t]:
			if len(rest) < 2 {
				return "", ErrUnresolvable
			}
			lookupChanged = lookupChanged || t == "-C" || t == "--chdir" || rest[1] == "PATH"
			rest = rest[2:]
		case strings.HasPrefix(t, "--unset="), strings.HasPrefix(t, "--chdir="):
			lookupChanged = lookupChanged || t == "--unset=PATH" || strings.HasPrefix(t, "--chdir=")
			rest = rest[1:]
		case envFlagOptions[t]:
			lookupChanged = lookupChanged || t == "-i" || t == "--ignore-environment"
			rest = rest[1:]
		case strings.HasPrefix(t, "-"):
			return "", ErrUnresolvable // unknown option: its operand is unknowable
		case strings.Contains(t, "="):
			if strings.HasPrefix(t, "PATH=") {
				lookupChanged = true
			}
			rest = rest[1:]
		default:
			// We cannot use Masir's cwd or PATH after env changes lookup.
			// A nested env would also inherit the changed lookup context.
			if lookupChanged && (!filepath.IsAbs(t) || filepath.Base(t) == "env") {
				return "", ErrUnresolvable
			}
			return resolveTokens(rest, desktopPath, depth+1)
		}
	}
	return "", ErrUnresolvable
}

var envOperandOptions = map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true}
var envFlagOptions = map[string]bool{"-i": true, "--ignore-environment": true, "-0": true, "--null": true, "-v": true, "--debug": true}

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
