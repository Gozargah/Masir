package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type componentImportClient struct {
	pb.DaemonServiceClient
	stream *componentImportStream
}

func (c componentImportClient) ImportDesktopUpdate(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[pb.ImportDesktopUpdateRequest, pb.ImportDesktopUpdateResponse], error) {
	return c.stream, nil
}

type componentImportStream struct {
	grpc.ClientStream
	messages []*pb.ImportDesktopUpdateRequest
	finished bool
	sendErr  error
	finalErr error
}

func (s *componentImportStream) Send(m *pb.ImportDesktopUpdateRequest) error {
	s.messages = append(s.messages, m)
	return s.sendErr
}
func (s *componentImportStream) CloseAndRecv() (*pb.ImportDesktopUpdateResponse, error) {
	s.finished = true
	return &pb.ImportDesktopUpdateResponse{Version: "1.2.0"}, s.finalErr
}

func TestInstalledComponentStreamReportsOldServiceStatus(t *testing.T) {
	dir := t.TempDir()
	for _, name := range updatebundle.Names(runtime.GOOS) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	descriptor, err := updatebundle.Create(dir, "1.2.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, updatebundle.DescriptorName), descriptor, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, updatebundle.SignatureName), []byte("signed"), 0o644); err != nil {
		t.Fatal(err)
	}
	stream := &componentImportStream{sendErr: io.EOF, finalErr: status.Error(codes.Unimplemented, "old service")}
	err = importComponentDirectory(context.Background(), componentImportClient{stream: stream}, dir, "1.2.0")
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("lost service error behind Send EOF: %v", err)
	}
	stream = &componentImportStream{}
	if err := importComponentDirectory(context.Background(), componentImportClient{stream: stream}, dir, "1.3.0"); err == nil || len(stream.messages) > 0 {
		t.Fatal("accepted components from a different app version")
	}
	if err := importComponentDirectory(context.Background(), componentImportClient{stream: stream}, dir, "1.2.0"); err != nil || !stream.finished {
		t.Fatalf("installed directory import failed: %v", err)
	}
}

func TestDesktopImportExpandsPackedGeoForExistingServices(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "corrupt"}[corrupt], func(t *testing.T) {
			prepared := t.TempDir()
			dir := filepath.Join(prepared, "bin")
			if runtime.GOOS == "darwin" {
				dir = filepath.Join(prepared, "Masir.app", "Contents", "Resources", "bin")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			raw := t.TempDir()
			want := map[string][]byte{}
			for _, name := range updatebundle.Names(runtime.GOOS) {
				want[name] = bytes.Repeat([]byte(name), 40<<10)
				os.WriteFile(filepath.Join(raw, name), want[name], 0o644)
			}
			if err := geopack.Pack(raw, dir); err != nil {
				t.Fatal(err)
			}
			bin := updatebundle.Names(runtime.GOOS)[0]
			os.WriteFile(filepath.Join(dir, bin), want[bin], 0o755)
			descriptor, err := updatebundle.Create(dir, "1.2.0", runtime.GOOS, runtime.GOARCH)
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, updatebundle.DescriptorName), descriptor, 0o644)
			os.WriteFile(filepath.Join(dir, updatebundle.SignatureName), []byte("verified by the service"), 0o644)
			if corrupt {
				p := filepath.Join(dir, "geosite.dat.gz")
				b, _ := os.ReadFile(p)
				os.WriteFile(p, b[:len(b)-5], 0o644)
			}
			stream := &componentImportStream{}
			err = importDesktopComponents(context.Background(), componentImportClient{stream: stream}, prepared, "1.2.0")
			if corrupt {
				if err == nil || stream.finished {
					t.Fatal("completed a corrupt import")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]byte{}
			for _, m := range stream.messages {
				if m.File != "" {
					got[m.File] = append(got[m.File], m.Data...)
				}
			}
			for name, data := range want {
				if !bytes.Equal(got[name], data) {
					t.Fatalf("wire bytes for %s differ", name)
				}
			}
		})
	}
}

func writeTestComponentDescriptor(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, updatebundle.DescriptorName), []byte(`{"version":"`+version+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyComponentsMatchSelectedDesktopBeforeHelper(t *testing.T) {
	dir := t.TempDir()
	old := installBundledDaemon
	defer func() { installBundledDaemon = old }()
	calls := 0
	installBundledDaemon = func(string, func()) error { calls++; return nil }
	writeTestComponentDescriptor(t, dir, "0.2.0")
	if err := installLegacyDesktopComponents(dir, "0.3.0", nil); err == nil || calls != 0 {
		t.Fatal("mismatched release reached helper")
	}
	if err := installLegacyDesktopComponents(dir, "v0.2.0", nil); err != nil || calls != 1 {
		t.Fatalf("matching release rejected: %v calls=%d", err, calls)
	}
	if err := os.WriteFile(filepath.Join(dir, updatebundle.DescriptorName), bytes.Repeat([]byte("x"), 16385), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installLegacyDesktopComponents(dir, "0.2.0", nil); err == nil || calls != 1 {
		t.Fatal("oversized descriptor reached helper")
	}
}
