//go:build windows

package proc

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// DetachAttrs starts the child without a console in its own process group,
// so it survives the window or agent command runner that started it.
func DetachAttrs(c *exec.Cmd) {
	const detachedProcess, newProcessGroup = 0x00000008, 0x00000200
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newProcessGroup, HideWindow: true}
}

func Terminate(p *os.Process) error {
	return exec.Command("taskkill", "/PID", strconv.Itoa(p.Pid), "/T", "/F").Run()
}
