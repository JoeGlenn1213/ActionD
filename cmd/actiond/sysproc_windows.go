//go:build windows

package main

import "syscall"

func actiondSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
