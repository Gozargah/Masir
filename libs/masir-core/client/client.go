// Package client is how a program reaches the masir daemon: finding the socket it
// answers on, dialing it, and starting a background daemon when none answers. The CLI
// and the desktop app share it.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Gozargah/Masir/libs/masir-core/paths"
	"github.com/Gozargah/Masir/libs/masir-core/transport"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// Where daemons may answer, where one starts, and where its output goes. Tests point
// them elsewhere.
var (
	sockets    = paths.Sockets
	userSocket = paths.SocketPath
	configDir  = paths.ConfigDir
)

// LiveSocket is where a daemon answers right now: a root daemon's socket if there is
// one, otherwise the socket of a daemon running as this user. Either way the caller
// needs no privileges to reach it.
func LiveSocket() (string, bool) {
	socks, err := sockets()
	if err != nil {
		return "", false
	}
	for _, s := range socks {
		if transport.Alive(s) {
			return s, true
		}
	}
	return "", false
}

// Alive reports whether any daemon answers.
func Alive() bool {
	_, ok := LiveSocket()
	return ok
}

// AliveAt reports whether a daemon answers on sock.
func AliveAt(sock string) bool { return transport.Alive(sock) }

// DialSocket connects to the daemon on sock, for a caller that knows which daemon it
// means: a root process replacing the service that serves a uid, say. Close the
// connection when done.
func DialSocket(sock string) (*grpc.ClientConn, pb.DaemonServiceClient, error) {
	conn, err := grpc.NewClient("passthrough:///masir-daemon",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return transport.DialContext(ctx, sock)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewDaemonServiceClient(conn), nil
}

// Dial connects to whichever daemon answers. The socket is looked up again on every
// (re)connect: a root daemon taking over from this user's daemon listens somewhere else.
// Close the returned connection when done with it.
func Dial() (*grpc.ClientConn, pb.DaemonServiceClient, error) { return DialWithSource("") }

// DialWithSource labels user-facing calls with a bounded client kind. It does not
// carry identity or change peer authorization.
func DialWithSource(source string) (*grpc.ClientConn, pb.DaemonServiceClient, error) {
	userSock, err := userSocket()
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient("passthrough:///masir-daemon",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			sock, ok := LiveSocket()
			if !ok {
				sock = userSock
			}
			return transport.DialContext(ctx, sock)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			if source == "gui" || source == "cli" {
				ctx = metadata.AppendToOutgoingContext(ctx, "masir-client", source)
			}
			return invoke(ctx, method, req, reply, cc, opts...)
		}))
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewDaemonServiceClient(conn), nil
}

// ErrorReason is the masir ErrorInfo reason on a status error, e.g. "ROOT_REQUIRED".
func ErrorReason(err error) string {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			return info.Reason
		}
	}
	return ""
}

// Start runs bin as a background daemon, as this user, unless something already answers
// (a background daemon or the root service). An idle daemon keeps Xray down until it is
// turned on. The daemon's output goes to masird.log in the config directory.
//
// A start that fails says why: the daemon's exit, or the socket it answered on when that
// isn't the one this command waited for. Waiting out the timeout in silence was how a
// daemon that started correctly looked like one that never started at all.
func Start(ctx context.Context, bin string, idle bool) error {
	if Alive() {
		return nil
	}
	sock, err := userSocket() // where a daemon starts, if none answers
	if err != nil {
		return err
	}
	dir, err := configDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "masird.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	args := []string{"daemon", "run"}
	if idle {
		args = append(args, "--idle")
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the masir daemon: %w", err)
	}
	// The child stays ours to wait on, rather than being released: a daemon that dies at
	// startup is a failure now, not a silent ten-second wait. It outlives this process
	// anyway (detached starts it in its own session).
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	timeout := time.NewTimer(startTimeout)
	defer timeout.Stop()
	tick := time.NewTicker(startPoll)
	defer tick.Stop()
	for {
		if Alive() {
			return nil
		}
		select {
		case err := <-exited:
			return &StartError{Socket: sock, LogPath: logPath, Exited: true, Exit: err,
				Tail: startTail(logPath, startTailLines)}
		case <-timeout.C:
			live, _ := LiveSocket()
			return &StartError{Socket: sock, Live: live, LogPath: logPath, Timeout: startTimeout,
				Tail: startTail(logPath, startTailLines)}
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

const (
	// startPoll is how often the sockets are tried while a daemon starts.
	startPoll = 150 * time.Millisecond
	// startTailLines is how much of the daemon's log a failure carries.
	startTailLines = 8
)

// startTimeout bounds a daemon that neither answers nor exits. A variable so tests
// don't spend ten seconds proving it.
var startTimeout = 10 * time.Second

// WaitFor polls ok until it holds, ctx ends, or timeout passes, in which case it fails
// with the failure text.
func WaitFor(ctx context.Context, ok func() bool, timeout time.Duration, failure string) error {
	deadline := time.Now().Add(timeout)
	for !ok() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New(failure)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}
