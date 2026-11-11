package service

import (
	"bytes"
	"strings"
	"testing"
)

func TestLegacyDefinitionMovesOnlyExecutable(t *testing.T) {
	original := []byte(`<plist><dict><key>ProgramArguments</key><array><string>` + LegacyDarwinBinary + `</string><string>daemon</string><string>run</string><string>--user</string><string>501</string></array><key>KeepAlive</key><true/></dict></plist>`)
	got, e := RewriteLegacyDefinition(original)
	if e != nil {
		t.Fatal(e)
	}
	want := strings.Replace(string(original), LegacyDarwinBinary, BinaryPath(), 1)
	if string(got) != want {
		t.Fatalf("definition lost other settings: %s", got)
	}
	again, e := RewriteLegacyDefinition(got)
	if e != nil || !bytes.Equal(again, got) {
		t.Fatalf("rewrite not idempotent: %v", e)
	}
	for _, bad := range []string{strings.Replace(string(original), LegacyDarwinBinary, "/tmp/Masir", 1), `<plist><dict></dict></plist>`,
		strings.Replace(string(original), "</dict>", "<key>Program</key><string>/tmp/override</string></dict>", 1),
		strings.Replace(string(original), "</dict>", "<key>ProgramArguments</key><array><string>/tmp/duplicate</string></array></dict>", 1),
		string(original) + "<invalid>",
		`<plist><dict><key>ProgramArguments</key><array></array></dict></plist>`, `<plist><dict><key>ProgramArguments</key><array><integer>5</integer></array></dict></plist>`} {
		if _, e := RewriteLegacyDefinition([]byte(bad)); e == nil {
			t.Fatal("accepted unexpected executable or invalid plist")
		}
	}
	if InstallDir() == LegacyDarwinDir {
		t.Fatal("fresh services still use a Homebrew-controlled parent")
	}
}
