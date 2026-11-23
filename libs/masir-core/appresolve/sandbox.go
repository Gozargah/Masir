package appresolve

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// A Flatpak export symlink points into the exact deployment. Inspect the
// corresponding /app file without running launchers or interpreting scripts.
func flatpakExecutable(desktopPath, command string) (string, error) {
	if command == "" || strings.Contains(command, "%") {
		return "", ErrUnresolvable
	}
	target, err := filepath.EvalSymlinks(desktopPath)
	if err != nil {
		return "", ErrUnresolvable
	}
	applications := filepath.Dir(target)
	export := filepath.Dir(filepath.Dir(applications))
	if filepath.Base(applications) != "applications" ||
		filepath.Base(filepath.Dir(applications)) != "share" || filepath.Base(export) != "export" {
		return "", ErrUnresolvable
	}
	root := filepath.Join(filepath.Dir(export), "files")
	if !strings.Contains(command, "/") {
		command = "/app/bin/" + command
	}
	relative, ok := strings.CutPrefix(filepath.ToSlash(filepath.Clean(command)), "/app/")
	if !ok {
		return "", ErrUnresolvable
	}
	actual, err := packagedExecutable(root, relative)
	if err != nil {
		return "", err
	}
	return "/app/" + filepath.ToSlash(actual), nil
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
