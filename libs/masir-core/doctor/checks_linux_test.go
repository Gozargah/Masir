//go:build linux

package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linuxSeams restores the Linux-only seams after the test.
func linuxSeams(t *testing.T) {
	t.Helper()
	oldRun, oldSEL, oldLabel, oldBin, oldV6, oldLook := runCommand, selinuxEnforcing, binaryLabelOK, serviceBinary, hasIPv6, lookPath
	t.Cleanup(func() {
		runCommand, selinuxEnforcing, binaryLabelOK, serviceBinary, hasIPv6, lookPath = oldRun, oldSEL, oldLabel, oldBin, oldV6, oldLook
	})
}

func TestLocalMTURejectionParsing(t *testing.T) {
	cases := map[string]string{
		"ping: local error: Message too long, mtu=1400":                  "mtu 1400",
		"ping: local error: message too long":                            "rejected locally",
		"ping: sendmsg: EMSGSIZE":                                        "rejected locally",
		"1 packets transmitted, 0 received, 100% packet loss, time 0ms":  "",
		"From 10.10.20.1 icmp_seq=1 Frag needed and DF set (mtu = 1420)": "mtu 1420",
	}
	for in, want := range cases {
		if got := localMTURejection(in); got != want {
			t.Errorf("localMTURejection(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckMTUCases(t *testing.T) {
	linuxSeams(t)
	lookPath = func(string) (string, error) { return "/usr/bin/ping", nil }
	env := Env{TunRunning: true}
	dfProbe := func(args []string) bool {
		for _, a := range args {
			if a == "do" {
				return true
			}
		}
		return false
	}

	// Small packets pass, full-size ones are rejected locally: the path MTU is
	// below 1500, which is not a blackhole and must not warn about stalls.
	runCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if dfProbe(args) {
			return []byte("ping: local error: Message too long, mtu=1400"), errors.New("exit status 2")
		}
		return []byte("1 packets transmitted, 1 received, 0% packet loss"), nil
	}
	r := checkMTU(context.Background(), env)
	if r.State != OK || !strings.Contains(r.Detail, "below 1500") {
		t.Fatalf("local rejection = %+v, want OK explaining the smaller MTU", r)
	}

	// Small packets pass, full-size ones vanish with no local error: blackhole.
	runCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if dfProbe(args) {
			return []byte("1 packets transmitted, 0 received, 100% packet loss"), errors.New("exit status 1")
		}
		return []byte("1 packets transmitted, 1 received, 0% packet loss"), nil
	}
	r = checkMTU(context.Background(), env)
	if r.State != Warn || !strings.Contains(r.Detail, "no reply") {
		t.Fatalf("blackhole = %+v, want Warn naming the missing reply", r)
	}

	// Nothing answers even a small ping: the probe proves nothing, so skip.
	runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("100% packet loss"), errors.New("exit status 1")
	}
	if r := checkMTU(context.Background(), env); r.State != Skip {
		t.Fatalf("no connectivity = %+v, want Skip", r)
	}

	// No ping binary at all: skip without pretending to have probed.
	lookPath = func(string) (string, error) { return "", errors.New("executable file not found") }
	if r := checkMTU(context.Background(), env); r.State != Skip || !strings.Contains(r.Detail, "not installed") {
		t.Fatalf("no ping = %+v, want Skip naming the missing tool", r)
	}
}

func TestCheckChurn(t *testing.T) {
	linuxSeams(t)
	runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(strings.Repeat("Xray 26.11.9 started\n", 8)), nil
	}
	if r := checkChurn(context.Background(), Env{}); r.State != Warn || !strings.Contains(r.Detail, "8 times") {
		t.Fatalf("churn = %+v, want Warn with the count", r)
	}
	runCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte(""), nil }
	if r := checkChurn(context.Background(), Env{}); r.State != OK {
		t.Fatalf("quiet journal = %+v, want OK", r)
	}
	runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("journalctl: not found")
	}
	if r := checkChurn(context.Background(), Env{}); r.State != Skip {
		t.Fatalf("unreadable journal = %+v, want Skip", r)
	}
}

func TestCheckDefaultRoute(t *testing.T) {
	linuxSeams(t)
	// 127.0.0.1 lives on lo on every platform, so it stands in for the tunnel
	// gateway that the check looks up.
	env := Env{TunRunning: true, TunGateway: "127.0.0.1/24"}
	runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("default via 192.168.1.1 dev wlo1 proto dhcp metric 600\ndefault via 127.0.0.1 dev lo proto static metric 10\n"), nil
	}
	if r := checkDefaultRoute(context.Background(), env); r.State != OK || !strings.Contains(r.Detail, "metric 10") {
		t.Fatalf("tunnel wins = %+v, want OK", r)
	}
	runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("default via 192.168.1.1 dev wlo1 proto dhcp metric 10\ndefault via 127.0.0.1 dev lo proto static metric 600\n"), nil
	}
	if r := checkDefaultRoute(context.Background(), env); r.State != Warn || !strings.Contains(r.Detail, "wlo1") {
		t.Fatalf("other interface wins = %+v, want Warn naming it", r)
	}
}

func TestCheckSELinuxLabel(t *testing.T) {
	linuxSeams(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "masir")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	serviceBinary = func() string { return bin }

	selinuxEnforcing = func() bool { return false }
	if r := checkSELinux(context.Background(), Env{}); r.State != Skip {
		t.Fatalf("no SELinux = %+v, want Skip", r)
	}
	selinuxEnforcing = func() bool { return true }
	binaryLabelOK = func(string) bool { return true }
	if r := checkSELinux(context.Background(), Env{}); r.State != OK {
		t.Fatalf("labelled = %+v, want OK", r)
	}
	binaryLabelOK = func(string) bool { return false }
	r := checkSELinux(context.Background(), Env{})
	if r.State != Fail || !strings.Contains(r.Fix, "restorecon") {
		t.Fatalf("bad label = %+v, want Fail with the restorecon fix", r)
	}
}

func TestCheckAVC(t *testing.T) {
	linuxSeams(t)
	selinuxEnforcing = func() bool { return true }
	runCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("type=AVC avc:  denied  { name_connect } for  pid=1 comm=\"masir\"\n"), nil
	}
	if r := checkAVC(context.Background(), Env{}); r.State != Warn || !strings.Contains(r.Detail, "1 SELinux denials") {
		t.Fatalf("denials = %+v, want Warn with the count", r)
	}
	runCommand = func(context.Context, string, ...string) ([]byte, error) { return []byte(""), nil }
	if r := checkAVC(context.Background(), Env{}); r.State != OK {
		t.Fatalf("clean = %+v, want OK", r)
	}
}

func TestCheckIPv6SkipsWithoutConfiguration(t *testing.T) {
	linuxSeams(t)
	hasIPv6 = func() bool { return false }
	r := checkIPv6(context.Background(), Env{TunRunning: true})
	if r.State != Skip || !strings.Contains(r.Detail, "no global IPv6") {
		t.Fatalf("no IPv6 = %+v, want Skip", r)
	}
}
