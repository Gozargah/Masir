package appresolve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFlatpakCommand(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{"--branch=stable --arch=x86_64 --command=spotify --file-forwarding com.spotify.Client @@u %U @@", "spotify"},
		{"--command=/app/opt/stremio/stremio com.stremio.Stremio %U", "/app/opt/stremio/stremio"},
		{"--command geary org.gnome.Geary", "geary"},
		{"--arch x86_64 --branch stable --command=spotify com.spotify.Client", "spotify"},
		{"org.example.App --command=worker", ""},
		{"--command=worker", ""},
	} {
		if got := flatpakCommand(Tokenize(tc.args)); got != tc.want {
			t.Errorf("flatpakCommand(%q) = %q, want %q", tc.args, got, tc.want)
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
		"flatpak run org.example.App --command=worker", // app argument, not a launcher option
		"flatpak install org.example.App",
		"snap run",
		"env FOO=bar",
		"env PATH=/opt/custom viewer", // the overridden PATH decides which viewer
		"env -C /opt/example ./bin/program",
		"env --chdir=/opt/example viewer",
		"env -u PATH viewer",
		"env -i viewer",
		"env PATH=/opt/custom /usr/bin/env viewer",
		"env -u", // option without its operand
		"%U",
	} {
		if got, err := Resolve(exec); !errors.Is(err, ErrUnresolvable) {
			t.Errorf("Resolve(%q) = %q, %v; want ErrUnresolvable", exec, got, err)
		}
	}
}

func TestEnvRejectsLookupChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix env options")
	}
	for _, command := range []string{
		"env -C /opt/example ls",
		"env --chdir=/opt/example ls",
		"env -u PATH ls",
		"env -i ls",
		"env PATH=/opt/custom /usr/bin/env ls",
	} {
		if _, err := Resolve(command); !errors.Is(err, ErrUnresolvable) {
			t.Errorf("Resolve(%q) = %v; want ErrUnresolvable", command, err)
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
	if _, err := d.Value(); !errors.Is(err, ErrUnresolvable) {
		t.Fatalf("unverified Flatpak command should be unresolvable, got %v", err)
	}
}

// Menus hide settings panels and actions; the picker must too.
func TestDesktopListed(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) Desktop {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := FromDesktop(p)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if !write("app.desktop", "[Desktop Entry]\nType=Application\nName=App\nExec=/bin/ls\n").Listed() {
		t.Error("a normal application must be listed")
	}
	if write("nodisplay.desktop", "[Desktop Entry]\nType=Application\nNoDisplay=true\nName=Panel\nExec=/bin/ls\n").Listed() {
		t.Error("NoDisplay entries must not be listed")
	}
	if write("hidden.desktop", "[Desktop Entry]\nType=Application\nHidden=true\nName=Gone\nExec=/bin/ls\n").Listed() {
		t.Error("Hidden entries must not be listed")
	}
	if write("link.desktop", "[Desktop Entry]\nType=Link\nName=Link\nURL=https://example.com\n").Listed() {
		t.Error("non-application entries must not be listed")
	}
}
