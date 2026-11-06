//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachAttrs puts the child in its own session, so it survives the
// terminal or agent command runner that started it.
func detachAttrs(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
