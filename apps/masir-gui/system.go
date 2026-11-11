package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"github.com/Gozargah/Masir/libs/masir-core/analytics"
	"github.com/Gozargah/Masir/libs/masir-core/paths"
)

// System hooks the pages use; tests replace them.
var (
	readClipboard = func() string {
		b, err := mygo.Clipboard.ReadFormat(transfer.Text)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	openAtLogin    = func() bool { return mygo.App.OpenAtLogin() }
	setOpenAtLogin = func(on bool) error { return mygo.App.SetOpenAtLogin(on) }
	quit           = func() { mygo.App.Quit() }
	openTerminalIn = openTerminalCommand
	openFileIn     = openFile
	// markNoticeShown leaves the marker the daemon waits for before sending anonymous
	// analytics, once the app has shown the notice.
	markNoticeShown = func() {
		dir, err := paths.ConfigDir()
		if err != nil || analytics.NoticeShown(dir) {
			return
		}
		_ = analytics.MarkNoticeShown(dir)
	}
)
