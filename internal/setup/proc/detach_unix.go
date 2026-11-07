//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// DetachAttrs puts the child in its own session, so it survives the
// terminal or agent command runner that started it.
func DetachAttrs(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func Terminate(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGKILL) }

// waitExit polls until p is gone (or d passes); an unreaped child of this
// process stays visible, which is harmless on Unix.
func waitExit(p *os.Process, d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if p.Signal(syscall.Signal(0)) != nil {
			return
		}
	}
}
