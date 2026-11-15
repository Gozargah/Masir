//go:build !windows

package xray

import "os/exec"

func configureProbeProcess(cmd *exec.Cmd) {}
