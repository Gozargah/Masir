package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleNameDecodesPlist(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "Sample.app", "Contents")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	xmlPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDisplayName</key>
	<string>Sample App</string>
	<key>CFBundleName</key>
	<string>Sample</string>
</dict>
</plist>`

	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(xmlPlist), 0o644); err != nil {
		t.Fatal(err)
	}

	name := bundleName(filepath.Join(dir, "Sample.app"))
	if name != "Sample App" {
		t.Fatalf("bundleName = %q, want %q", name, "Sample App")
	}
}
