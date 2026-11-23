package appresolve

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScopeOf(t *testing.T) {
	cases := []struct {
		name, line string
		want       Scope
	}{
		{"flatpak instance",
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-flatpak-com.spotify.Client-974637243.scope",
			Scope{Kind: "flatpak", ID: "com.spotify.Client"}},
		{"cgroup v1, several records",
			"1:name=systemd:/user.slice/x/app-flatpak-com.example.App-42.scope\n2:cpu:/\n",
			Scope{Kind: "flatpak", ID: "com.example.App"}},
		{"snap instance",
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/snap.solvespace.solvespace-fd659ed2-ecca-42a1-bac9-bccd6c2d3721.scope",
			Scope{Kind: "snap", ID: "solvespace"}},
		{"snap default app",
			"0::/user.slice/x/snap.suite.suite-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.scope\n",
			Scope{Kind: "snap", ID: "suite"}},
		{"snap second app",
			"0::/user.slice/x/snap.suite.worker-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.scope\n",
			Scope{Kind: "snap", ID: "suite.worker"}},
		{"snap instance, default app",
			"0::/user.slice/x/snap.firefox_firefox.firefox-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.scope\n",
			Scope{Kind: "snap", ID: "firefox_firefox"}},
		{"snap package freezer group before the app scope",
			"7:freezer:/snap.suite\n4:name=systemd:/user.slice/x/snap.suite.worker-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.scope\n",
			Scope{Kind: "snap", ID: "suite.worker"}},
		{"snap package freezer group alone",
			"7:freezer:/snap.suite\n",
			Scope{}},
		{"ordinary service",
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/hermes-gateway.service",
			Scope{}},
		{"snapd daemon",
			"0::/system.slice/snapd.service",
			Scope{}},
	}
	for _, c := range cases {
		if got := ScopeOf(c.line); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestObservedApps(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup layout is a Linux detail")
	}
	root := t.TempDir()
	write := func(pid, cgroup, exe string) {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte(cgroup), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(exe, filepath.Join(dir, "exe")); err != nil {
			t.Fatal(err)
		}
	}
	flat := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-flatpak-com.spotify.Client-42.scope\n"
	snap := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/snap.solvespace.solvespace-fd659ed2-ecca-42a1-bac9-bccd6c2d3721.scope\n"
	write("100", flat, "/app/extra/share/spotify/spotify")
	write("101", flat, "/usr/bin/bwrap") // the sandbox's own tool: not the app
	write("102", snap, "/snap/solvespace/current/bin/solvespace")
	write("1", "0::/init.scope\n", "/usr/bin/systemd")

	got := observedApps(root)
	if names := got["flatpak:com.spotify.Client"]; len(names) != 1 || names[0] != "spotify" {
		t.Errorf("flatpak scope = %v, want [spotify] (bwrap excluded)", names)
	}
	if names := got["snap:solvespace"]; len(names) != 1 || names[0] != "solvespace" {
		t.Errorf("snap scope = %v, want [solvespace]", names)
	}
	if len(got) != 2 {
		t.Errorf("scopes = %v, want exactly the two apps", got)
	}
}

func TestBestName(t *testing.T) {
	if got := BestName([]string{"python3", "python3"}); got != "" {
		t.Errorf("interpreter-only app = %q; a rule on it would catch every Python app", got)
	}
	if got := BestName([]string{"ruby3.3"}); got != "" {
		t.Errorf("versioned interpreter = %q; a rule on it would catch every Ruby app", got)
	}
	if got := BestName([]string{"spotify", "spotify", "spotify-helper"}); got != "spotify" {
		t.Errorf("best name = %q, want spotify", got)
	}
	if got := BestName(nil); got != "" {
		t.Errorf("empty = %q", got)
	}
}

// The snap mount directory is configurable; the host path shows up for
// classic-confined snaps.
func TestInsideSandboxSnapMount(t *testing.T) {
	for _, exe := range []string{
		"/app/bin/spotify",
		"/snap/solvespace/current/bin/solvespace",
		"/var/lib/snapd/snap/solvespace/current/bin/solvespace",
	} {
		if !insideSandbox(exe) {
			t.Errorf("%q should count as sandboxed", exe)
		}
	}
	for _, exe := range []string{"/usr/bin/ls", "/usr/lib/snapd/snap-confine"} {
		if insideSandbox(exe) {
			t.Errorf("%q is not an app executable", exe)
		}
	}
}

func TestDesktopScope(t *testing.T) {
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
	if got := write("a.desktop", "[Desktop Entry]\nName=A\nExec=/usr/bin/flatpak run --command=a com.a.App\nX-Flatpak=com.a.App\n").Scope(); got != (Scope{Kind: "flatpak", ID: "com.a.App"}) {
		t.Errorf("flatpak scope = %+v", got)
	}
	if got := write("b.desktop", "[Desktop Entry]\nName=B\nExec=/snap/bin/bitwarden %U\n").Scope(); got != (Scope{Kind: "snap", ID: "bitwarden"}) {
		t.Errorf("snap scope = %+v", got)
	}
	if got := write("c.desktop", "[Desktop Entry]\nName=C\nExec=env FOO=bar /var/lib/snapd/snap/bin/solvespace\n").Scope(); got != (Scope{Kind: "snap", ID: "solvespace"}) {
		t.Errorf("snapd scope = %+v", got)
	}
	if got := write("d.desktop", "[Desktop Entry]\nName=D\nExec=/usr/bin/ls\n").Scope(); got != (Scope{}) {
		t.Errorf("plain entry scope = %+v", got)
	}
}
