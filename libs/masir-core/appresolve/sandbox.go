package appresolve

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v2"
)

// A Flatpak export symlink points into the exact deployment. Inspect the
// corresponding /app file without running launchers.
func flatpakExecutable(desktopPath, command string) (string, error) {
	if command == "" || strings.Contains(command, "%") {
		return "", ErrUnresolvable
	}
	root := flatpakFiles(desktopPath)
	if root == "" {
		return "", ErrUnresolvable
	}
	if !strings.Contains(command, "/") {
		command = "/app/bin/" + command
	}
	relative, ok := strings.CutPrefix(filepath.ToSlash(filepath.Clean(command)), "/app/")
	if !ok {
		return "", ErrUnresolvable
	}
	if name, err := deploymentExecutable(root, relative); err == nil {
		return name, nil
	}
	// The entry is often a wrapper script that starts the real program
	// (Spotify's /app/bin/spotify). Follow the sandbox paths it names to a
	// native executable inside the deployment. A wrapper that only launches an
	// interpreter (a Python app) still has no nameable identity and stays out.
	for _, target := range wrapperTargets(root, relative) {
		if name, err := deploymentExecutable(root, target); err == nil {
			return name, nil
		}
	}
	return "", ErrUnresolvable
}

// deploymentExecutable verifies a native executable inside a deployment. A
// symlink whose target is an absolute sandbox path (/app/...) cannot be resolved
// from the host, so it is mapped back into the deployment instead.
func deploymentExecutable(root, relative string) (string, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrUnresolvable
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	for hops := 0; hops < 8; hops++ {
		if rel, err := filepath.Rel(root, path); err != nil || !filepath.IsLocal(rel) {
			return "", ErrUnresolvable
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", ErrUnresolvable
		}
		if info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", ErrUnresolvable
		}
		switch {
		case strings.HasPrefix(target, "/app/"):
			path = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(target, "/app/")))
		case filepath.IsAbs(target):
			return "", ErrUnresolvable
		default:
			path = filepath.Join(filepath.Dir(path), target)
		}
	}
	if rel, err := filepath.Rel(root, path); err != nil || !filepath.IsLocal(rel) {
		return "", ErrUnresolvable
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", ErrUnresolvable
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrUnresolvable
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "\x7fELF" {
		return "", ErrUnresolvable
	}
	return filepath.Base(path), nil
}

// wrapperPath matches the sandbox paths a launcher script can name.
var wrapperPath = regexp.MustCompile(`/app/[A-Za-z0-9@._+-][A-Za-z0-9@._+-]*(?:/[A-Za-z0-9@._+-]+)*`)

// wrapperTargets lists the /app paths a script entry mentions, in order.
func wrapperTargets(root, relative string) []string {
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || (len(data) >= 4 && string(data[:4]) == "\x7fELF") {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, match := range wrapperPath.FindAllString(string(data), 64) {
		rel := strings.TrimPrefix(match, "/app/")
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	return out
}

func flatpakFiles(desktopPath string) string {
	target, err := filepath.EvalSymlinks(desktopPath)
	if err != nil {
		return ""
	}
	applications := filepath.Dir(target)
	export := filepath.Dir(filepath.Dir(applications))
	if filepath.Base(applications) != "applications" ||
		filepath.Base(filepath.Dir(applications)) != "share" || filepath.Base(export) != "export" {
		return ""
	}
	return filepath.Join(filepath.Dir(export), "files")
}

func installedSnapExecutable(name string) (string, error) {
	for _, root := range []string{"/snap", "/var/lib/snapd/snap"} {
		if value, err := snapExecutable(root, name); err == nil {
			return value, nil
		}
	}
	return "", ErrUnresolvable
}

func snapExecutable(mount, name string) (string, error) {
	instance, app, found := strings.Cut(name, ".")
	if strings.ContainsAny(name, "/\\%") || instance == "" || instance == ".." {
		return "", ErrUnresolvable
	}
	if !found {
		app, _, _ = strings.Cut(instance, "_")
	}
	root := filepath.Join(mount, instance, "current")
	data, err := os.ReadFile(filepath.Join(root, "meta", "snap.yaml"))
	if err != nil {
		return "", ErrUnresolvable
	}
	var metadata struct {
		Apps map[string]struct {
			Command string
		}
	}
	if err := yaml.Unmarshal(data, &metadata); err != nil {
		return "", ErrUnresolvable
	}
	fields := strings.Fields(metadata.Apps[app].Command)
	if len(fields) == 0 || filepath.IsAbs(fields[0]) {
		return "", ErrUnresolvable
	}
	actual, err := packagedExecutable(root, fields[0])
	if err != nil {
		return "", err
	}
	// A basename survives snap refreshes, which change the mounted revision.
	return filepath.Base(actual), nil
}

// Only accept a native executable inside the deployment. Shell wrappers have
// no reliable process identity, and a symlink can change the actual basename.
func packagedExecutable(root, relative string) (string, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrUnresolvable
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", ErrUnresolvable
	}
	actual, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(actual) {
		return "", ErrUnresolvable
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", ErrUnresolvable
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrUnresolvable
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "\x7fELF" {
		return "", ErrUnresolvable
	}
	return actual, nil
}
