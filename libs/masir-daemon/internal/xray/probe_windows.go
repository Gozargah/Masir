package xray

import (
	"os/exec"
	"syscall"
)

// A service or tray-triggered probe must not flash a console window.
func configureProbeProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
