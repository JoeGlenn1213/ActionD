// Copyright (c) 2025 JoeGlenn1213
// ActionD MCP Server - Local server lifecycle control

package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// LifecycleController controls ActionD daemon lifecycle from MCP.
type LifecycleController interface {
	Start(ctx context.Context) (string, error)
	Stop(ctx context.Context) (string, error)
	IsRunning() bool
}

// LocalLifecycleController controls the local ActionD daemon by invoking
// the ActionD binary directly.
type LocalLifecycleController struct {
	binaryPath string
}

// NewLocalLifecycleController creates a local lifecycle controller.
func NewLocalLifecycleController(binaryPath string) *LocalLifecycleController {
	return &LocalLifecycleController{
		binaryPath: binaryPath,
	}
}

// Start launches ActionD in daemon mode.
func (c *LocalLifecycleController) Start(ctx context.Context) (string, error) {
	if c.binaryPath == "" {
		return "", fmt.Errorf("actiond binary path is empty")
	}
	//nolint:gosec // G204: binaryPath is trusted (os.Executable from caller)
	cmd := exec.CommandContext(ctx, c.binaryPath, "start", "--daemon")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Stop terminates ActionD daemon.
func (c *LocalLifecycleController) Stop(ctx context.Context) (string, error) {
	if c.binaryPath == "" {
		return "", fmt.Errorf("actiond binary path is empty")
	}
	//nolint:gosec // G204: binaryPath is trusted (os.Executable from caller)
	cmd := exec.CommandContext(ctx, c.binaryPath, "stop")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// IsRunning returns true if ActionD daemon is alive.
func (c *LocalLifecycleController) IsRunning() bool {
	pidFile := getActionDPidFilePath()
	pid := readActionDPid(pidFile)
	if pid == 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func getActionDPidFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".localgithub", "actions", "actiond.pid")
	}
	return filepath.Join(home, ".localgithub", "actions", "actiond.pid")
}

func readActionDPid(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}
