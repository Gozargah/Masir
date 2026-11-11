package server

import (
	"context"
	"encoding/json"
	"github.com/Gozargah/Masir/libs/masir-core/updatebundle"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDesktopImportRejectsUnsignedMetadataBeforeInstallation(t *testing.T) {
	s := newTestServer(t)
	s.initUpdates(t.TempDir())
	client := dialInProcess(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := client.ImportDesktopUpdate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&pb.ImportDesktopUpdateRequest{ManifestBytes: []byte(`{"version":"99.0.0"}`), Signature: []byte("attacker")})
	if _, err := stream.CloseAndRecv(); err == nil {
		t.Fatal("accepted unsigned update")
	}
	if !s.upd.installMu.TryLock() {
		t.Fatal("failed import retained installer lock")
	}
	s.upd.installMu.Unlock()
}
func TestDesktopImportRequiresBoundedDeadline(t *testing.T) {
	s := newTestServer(t)
	s.initUpdates(t.TempDir())
	stream, err := dialInProcess(t, s).ImportDesktopUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&pb.ImportDesktopUpdateRequest{})
	if _, err := stream.CloseAndRecv(); err == nil {
		t.Fatal("unbounded upload accepted")
	}
}

func TestDesktopUploadVerifiesEveryByteAndRejectsMalformedStreams(t *testing.T) {
	if !desktopStagingTestAllowed() {
		t.Skip("protected Windows uploads require an elevated test process")
	}
	source := t.TempDir()
	for _, name := range updatebundle.Names(runtime.GOOS) {
		os.WriteFile(filepath.Join(source, name), []byte("data"), 0o644)
	}
	b, err := updatebundle.Create(source, "1.0.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	var d updatebundle.Descriptor
	json.Unmarshal(b, &d)
	for _, kind := range []string{"valid", "tampered executable", "tampered geo", "truncated", "wrong order", "path traversal", "extra data", "empty chunk", "oversize chunk"} {
		t.Run(kind, func(t *testing.T) {
			var messages []*pb.ImportDesktopUpdateRequest
			for _, name := range updatebundle.Names(runtime.GOOS) {
				messages = append(messages, &pb.ImportDesktopUpdateRequest{File: name, Data: []byte("data")})
			}
			switch kind {
			case "tampered executable":
				messages[0].Data = []byte("evil")
			case "tampered geo":
				messages[1].Data = []byte("evil")
			case "truncated":
				messages = messages[:2]
			case "wrong order":
				messages[0], messages[1] = messages[1], messages[0]
			case "path traversal":
				messages[0].File = "../masir"
			case "extra data":
				messages = append(messages, messages[0])
			case "empty chunk":
				messages[0].Data = nil
			case "oversize chunk":
				messages[0].Data = make([]byte, 256<<10+1)
			}
			index := 0
			err := receiveDesktopFiles(t.TempDir(), d, func() (*pb.ImportDesktopUpdateRequest, error) {
				if index == len(messages) {
					return nil, io.EOF
				}
				m := messages[index]
				index++
				return m, nil
			})
			if (err == nil) != (kind == "valid") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
