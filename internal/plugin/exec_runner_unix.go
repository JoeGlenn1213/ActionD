// Copyright (c) 2025 JoeGlenn1213
//go:build !windows
// +build !windows

package plugin

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// Kill the entire process group
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
