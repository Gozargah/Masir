package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
)

type jsonCommandDaemon struct {
	pb.DaemonServiceClient
	subs       []*pb.Subscription
	selectErr  error
	selections int
}

func (f *jsonCommandDaemon) ListSubscriptions(context.Context, *pb.ListSubscriptionsRequest, ...grpc.CallOption) (*pb.ListSubscriptionsResponse, error) {
	return &pb.ListSubscriptionsResponse{Subscriptions: f.subs}, nil
}
func (f *jsonCommandDaemon) SelectConfig(context.Context, *pb.SelectConfigRequest, ...grpc.CallOption) (*pb.SelectConfigResponse, error) {
	f.selections++
	return &pb.SelectConfigResponse{}, f.selectErr
}
func (f *jsonCommandDaemon) GetActiveConfig(context.Context, *pb.GetActiveConfigRequest, ...grpc.CallOption) (*pb.GetActiveConfigResponse, error) {
	return nil, errors.New("active lookup failed")
}

// Exercise runE as well as the command: advice is rendered only when errors are
// reported. The embedded nil client also catches any unexpected recovery RPC.
func runJSONCommand(t *testing.T, f *jsonCommandDaemon, argv ...string) (string, string, int) {
	t.Helper()
	oldClient, oldExit, oldErr := cachedClient, exitStatus, lastErr
	cachedClient, exitStatus = f, 0
	defer func() { cachedClient, exitStatus, lastErr = oldClient, oldExit, oldErr }()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldStderr }()
	executeErr := executeCLI(context.Background(), argv)
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = oldOut, oldStderr
	out, _ := io.ReadAll(outR)
	diagnostic, _ := io.ReadAll(errR)
	outR.Close()
	errR.Close()
	if executeErr != nil {
		exitStatus = 1 // main exits 1 when Fang returns a usage error.
	}
	return string(out), string(diagnostic), exitStatus
}
func TestJSONCommandErrorsKeepStdoutEmpty(t *testing.T) {
	sub := &pb.Subscription{Id: "sub1", Configs: []*pb.SubscriptionConfig{{Id: "cfg1", Name: "Germany"}}}
	tests := []struct {
		name   string
		argv   []string
		daemon *jsonCommandDaemon
		want   string
	}{
		{"extra arguments", []string{"use", "cfg1", "extra", "--json"}, &jsonCommandDaemon{}, "Accepts at most 1 arg"},
		{"alias extra arguments", []string{"sub", "use", "cfg1", "extra", "--json"}, &jsonCommandDaemon{}, "Accepts at most 1 arg"},
		{"list arguments", []string{"sub", "list", "extra", "--json"}, &jsonCommandDaemon{}, "Unknown command"},
		{"empty", []string{"use", "cfg1", "--json"}, &jsonCommandDaemon{}, "There are no subscriptions"},
		{"empty fastest", []string{"use", "--fastest", "--json"}, &jsonCommandDaemon{}, "There are no subscriptions"},
		{"missing id", []string{"use", "--json"}, &jsonCommandDaemon{subs: []*pb.Subscription{sub}}, "pass a proxy id"},
		{"broken rules", []string{"use", "cfg1", "--json"}, &jsonCommandDaemon{subs: []*pb.Subscription{sub}, selectErr: brokenErr(t)}, "can't be built"},
		{"alias broken rules", []string{"sub", "use", "cfg1", "--json"}, &jsonCommandDaemon{subs: []*pb.Subscription{sub}, selectErr: brokenErr(t)}, "can't be built"},
		{"active lookup", []string{"sub", "list", "--json"}, &jsonCommandDaemon{}, "active lookup failed"},
		{"bare sub", []string{"sub", "--json"}, &jsonCommandDaemon{}, "active lookup failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, diagnostic, code := runJSONCommand(t, tt.daemon, tt.argv...)
			if out != "" {
				t.Errorf("stdout = %q, want empty", out)
			}
			if code != 1 || !strings.Contains(diagnostic, tt.want) {
				t.Errorf("exit=%d stderr=%q, want exit 1 containing %q", code, diagnostic, tt.want)
			}
			if strings.Contains(diagnostic, "\x1b") {
				t.Errorf("ANSI in stderr: %q", diagnostic)
			}
			if tt.daemon.selectErr != nil && tt.daemon.selections != 1 {
				t.Errorf("selection retried %d times", tt.daemon.selections)
			}
		})
	}
}
func TestJSONUseCommandSuccess(t *testing.T) {
	f := &jsonCommandDaemon{subs: []*pb.Subscription{{Id: "sub1", Configs: []*pb.SubscriptionConfig{{Id: "cfg1", Name: "Germany"}}}}}
	out, diagnostic, code := runJSONCommand(t, f, "use", "cfg1", "--json")
	if code != 0 || diagnostic != "" || !json.Valid([]byte(out)) || f.selections != 1 {
		t.Fatalf("exit=%d stderr=%q stdout=%q selections=%d", code, diagnostic, out, f.selections)
	}
}

func TestJSONInputKeepsHumanUsageReporting(t *testing.T) {
	for _, command := range []string{"add", "edit"} {
		t.Run(command, func(t *testing.T) {
			out, diagnostic, code := runJSONCommand(t, &jsonCommandDaemon{}, "rules", command, "one", "extra", "--json", `{"domain":["example.com"]}`)
			if code != 1 || !strings.Contains(out, "See the usage:") || diagnostic == "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, diagnostic)
			}
		})
	}
}
func TestJSONOutputRequiresBooleanFlag(t *testing.T) {
	for _, tt := range []struct {
		argv []string
		want bool
	}{
		{[]string{"use", "--json"}, true},
		{[]string{"use", "--json=false"}, false},
		{[]string{"use"}, false},
		{[]string{"rules", "add", "--json", `{"domain":["example.com"]}`}, false},
		{[]string{"rules", "edit", "--json", `{"domain":["example.com"]}`}, false},
	} {
		root := rootCmd()
		cmd, rest, err := root.Find(tt.argv)
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Fatal(err)
		}
		if got := jsonOutput(cmd); got != tt.want {
			t.Errorf("jsonOutput(%v)=%v, want %v", tt.argv, got, tt.want)
		}
	}
}
