package appresolve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveLaunchers(t *testing.T) {
	cases := []struct{ name, exec, want string }{
		{"flatpak named command", "/usr/bin/flatpak run --branch=stable --arch=x86_64 --command=spotify --file-forwarding com.spotify.Client @@u %U @@", "spotify"},
		{"flatpak absolute command", "/usr/bin/flatpak run --command=/app/opt/stremio/stremio com.stremio.Stremio %U", "/app/opt/stremio/stremio"},
		{"flatpak spaced flag form", "/usr/bin/flatpak run --command geary org.gnome.Geary", "geary"},
		{"snap run", "snap run bitwarden --no-sandbox", "bitwarden"},
		{"snap bin symlink form", "/snap/bin/bitwarden %U", "bitwarden"},
		{"env assignment", "env BAMF_DESKTOP_FILE_HINT=x flatpak run --command=spotify com.spotify.Client", "spotify"},
	}
	for _, c := range cases {
		got, err := Resolve(c.exec)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A launcher that cannot name its program must say so — a guessed value would
// match the wrong process or none at all.
func TestResolveUnresolvable(t *testing.T) {
	for _, exec := range []string{
		"",
		"sh -c 'spotify --uri=%U'",
		"bash -c /usr/bin/spotify",
		"gtk-launch spotify",
		"/usr/bin/flatpak run com.spotify.Client",
		"/usr/bin/flatpak run --command= com.spotify.Client",
		"snap run",
		"env FOO=bar",
		"%U",
	} {
		if got, err := Resolve(exec); !errors.Is(err, ErrUnresolvable) {
			t.Errorf("Resolve(%q) = %q, %v; want ErrUnresolvable", exec, got, err)
		}
	}
}

func TestResolvePlainExecutable(t *testing.T) {
	// LookPath finds the host's own binary; compare against the platform's
	// executable name (Windows appends .exe).
	probe := "ls"
	if runtime.GOOS == "windows" {
		probe = "cmd"
	}
	for _, exec := range []string{probe + " -la", probe + " %U"} {
		got, err := Resolve(exec)
		if err != nil {
			t.Errorf("Resolve(%q): %v", exec, err)
			continue
		}
		if !filepath.IsAbs(got) || strings.TrimSuffix(strings.ToLower(filepath.Base(got)), ".exe") != probe {
			t.Errorf("Resolve(%q) = %q; want an absolute %s executable", exec, got, probe)
		}
	}
	if runtime.GOOS != "windows" {
		if got, err := Resolve("/bin/ls"); err != nil || filepath.Base(got) != "ls" {
			t.Errorf("absolute entry resolved to %q, %v; want the ls path", got, err)
		}
	}
}

// A quoted executable path keeps its spaces (desktop entries quote them).
func TestResolveQuotedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test path separator would collide with desktop-entry escaping")
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "my app")
	if err := os.WriteFile(app, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve("\"" + app + "\" %U")
	if err != nil {
		t.Fatal(err)
	}
	// Resolution follows symlinks: /var is a link to /private/var on macOS.
	want := app
	if real, err := filepath.EvalSymlinks(app); err == nil {
		want = real
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFromDesktop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spotify.desktop")
	content := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Spotify\n" +
		"Exec=/usr/bin/flatpak run --branch=stable --arch=x86_64 --command=spotify --file-forwarding com.spotify.Client @@u %U @@\n" +
		"\n[Desktop Action NewWindow]\nName=New Window\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := FromDesktop(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Spotify" {
		t.Errorf("name = %q, want Spotify", d.Name)
	}
	if d.Exec == "" || !strings.Contains(d.Exec, "--command=spotify") {
		t.Errorf("exec = %q; want the launcher line", d.Exec)
	}
	if v, err := d.Value(); err != nil || v != "spotify" {
		t.Errorf("value = %q, %v; want spotify", v, err)
	}
}
