//go:build !windows

package client

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

type fakeDaemon struct {
	pb.UnimplementedDaemonServiceServer
}

func (fakeDaemon) GetXrayVersion(context.Context, *pb.GetXrayVersionRequest) (*pb.GetXrayVersionResponse, error) {
	return &pb.GetXrayVersionResponse{Version: "v1.2.3"}, nil
}

// serve answers on a socket of its own, which is the only one the client looks at.
func serve(t *testing.T) (func(), string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mc") // short: unix socket paths are
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := dir + "/daemon.sock"
	oldSockets, oldUser := sockets, userSocket
	sockets = func() ([]string, error) { return []string{sock}, nil }
	userSocket = func() (string, error) { return sock, nil }
	t.Cleanup(func() { sockets, userSocket = oldSockets, oldUser })
	l, srv := listen(t, sock)
	return func() { l.Close(); srv.Stop() }, sock
}

// listen returns the listener as well as the server: closing the listener is what
// makes the socket unreachable, and Stop alone can race with the Serve goroutine.
func listen(t *testing.T, sock string) (net.Listener, *grpc.Server) {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	pb.RegisterDaemonServiceServer(srv, fakeDaemon{})
	go srv.Serve(l)
	t.Cleanup(func() { l.Close(); srv.Stop() })
	return l, srv
}

func TestDialReachesTheLiveDaemon(t *testing.T) {
	serve(t)
	if !Alive() {
		t.Fatal("the daemon answers, but Alive says it doesn't")
	}
	conn, c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.GetXrayVersion(ctx, &pb.GetXrayVersionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "v1.2.3" {
		t.Fatalf("version = %q", res.Version)
	}
}

func TestDialSurvivesTheDaemonRestarting(t *testing.T) {
	stop, sock := serve(t)
	conn, c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stop()
	if Alive() {
		t.Fatal("Alive after the daemon stopped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.GetXrayVersion(ctx, &pb.GetXrayVersionRequest{}); status.Code(err) == codes.OK {
		t.Fatal("a call succeeded with no daemon")
	}

	os.Remove(sock)
	listen(t, sock)

	// The same client redials on its own once a daemon answers again.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := c.GetXrayVersion(ctx, &pb.GetXrayVersionRequest{}, grpc.WaitForReady(true))
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client never reconnected: %v", err)
		}
	}
}

func TestErrorReasonIgnoresPlainErrors(t *testing.T) {
	if r := ErrorReason(status.Error(codes.Internal, "boom")); r != "" {
		t.Fatalf("reason = %q", r)
	}
}

func TestWaitForGivesUp(t *testing.T) {
	err := WaitFor(context.Background(), func() bool { return false }, 200*time.Millisecond, "never")
	if err == nil || err.Error() != "never" {
		t.Fatalf("err = %v", err)
	}
}

// startAround points the client at temporary sockets and a temporary config directory,
// so Start never finds a real daemon or writes over a real log.
func startAround(t *testing.T, socks ...string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ms") // short: unix socket paths are
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	oldSockets, oldUser, oldConfig, oldTimeout := sockets, userSocket, configDir, startTimeout
	t.Cleanup(func() { sockets, userSocket, configDir, startTimeout = oldSockets, oldUser, oldConfig, oldTimeout })
	sockets = func() ([]string, error) { return socks, nil }
	userSocket = func() (string, error) { return dir + "/daemon.sock", nil }
	configDir = func() (string, error) { return dir, nil }
	return dir
}

// script writes a shell script that stands in for masir, and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/masir"
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A daemon that dies at startup is reported when it dies, with its exit status — not as
// a silent ten-second timeout that blames the log.
func TestStartReportsADaemonThatDiesAtStartup(t *testing.T) {
	dir := startAround(t) // no socket: nothing answers anywhere
	startTimeout = 10 * time.Second

	began := time.Now()
	err := Start(context.Background(), script(t, "exit 7"), true)
	took := time.Since(began)

	var se *StartError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StartError", err)
	}
	if se.Exit == nil {
		t.Fatalf("didn't report the daemon's exit: %v", err)
	}
	if !se.Exited {
		t.Fatal("reported a timeout for a daemon that exited")
	}
	if se.LogPath != dir+"/masird.log" {
		t.Fatalf("log = %q, want %q", se.LogPath, dir+"/masird.log")
	}
	if took > 2*time.Second {
		t.Fatalf("waited %s for a daemon that was already gone", took)
	}
}

// A daemon that stays up but never binds still fails, and its own words — not just the
// log's path — come with the failure.
func TestStartCarriesWhatTheDaemonWrote(t *testing.T) {
	dir := startAround(t)
	startTimeout = 400 * time.Millisecond

	err := Start(context.Background(), script(t, `echo "boom: cannot listen" >&2; exec sleep 1`), true)

	var se *StartError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StartError", err)
	}
	if se.Timeout == 0 {
		t.Fatalf("reported an exit, not a timeout: %v", err)
	}
	if !strings.Contains(se.Detail(), "boom: cannot listen") {
		t.Fatalf("detail = %q, want the daemon's line", se.Detail())
	}
	if se.LogPath != dir+"/masird.log" {
		t.Fatalf("log = %q", se.LogPath)
	}
}

// A daemon that answers on a socket this command didn't start is still a daemon that
// answers: the start succeeds, and the caller dials the live socket as usual.
func TestStartAcceptsADaemonThatAnswersElsewhere(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ms")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	live := dir + "/live.sock"

	oldSockets, oldUser, oldConfig, oldTimeout := sockets, userSocket, configDir, startTimeout
	t.Cleanup(func() { sockets, userSocket, configDir, startTimeout = oldSockets, oldUser, oldConfig, oldTimeout })
	sockets = func() ([]string, error) { return []string{live}, nil }
	userSocket = func() (string, error) { return dir + "/daemon.sock", nil }
	configDir = func() (string, error) { return dir, nil }
	startTimeout = 3 * time.Second

	// The daemon appears a moment after this command looked, as a takeover's does.
	ready := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		l, err := net.Listen("unix", live)
		if err != nil {
			t.Errorf("listen: %v", err)
			close(ready)
			return
		}
		close(ready)
		<-time.After(time.Second)
		l.Close()
	}()

	began := time.Now()
	if err := Start(context.Background(), script(t, "exec sleep 2"), true); err != nil {
		t.Fatalf("Start = %v, want the daemon that answered on %s", err, live)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("Start took %s", took)
	}
	<-ready
}
