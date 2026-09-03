// Copyright (c) 2025 JoeGlenn1213
//go:build windows
// +build windows

package plugin

import (
	"os/exec"
	"strconv"
)

func setProcessGroup(cmd *exec.Cmd) {
	// Not implemented for Windows, exec.CommandContext handles basic killing
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// Use taskkill to kill the process tree on Windows
		killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		_ = killCmd.Run()
	}
}
