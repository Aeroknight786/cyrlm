//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// DetachAttrs puts the child in its own session, so it survives the
// terminal or agent command runner that started it.
func DetachAttrs(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func Terminate(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGKILL) }

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
