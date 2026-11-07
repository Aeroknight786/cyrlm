//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// detachAttrs starts the child without a console in its own process group,
// so it survives the window or agent command runner that started it.
func detachAttrs(c *exec.Cmd) {
	const detachedProcess, newProcessGroup = 0x00000008, 0x00000200
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newProcessGroup, HideWindow: true}
}
