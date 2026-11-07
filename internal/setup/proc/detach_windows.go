//go:build windows

package proc

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
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

// waitExit waits for p to exit: Windows keeps an engine's DLLs locked
// until then, so undo could not remove the engine directory.
func waitExit(p *os.Process, d time.Duration) {
	done := make(chan struct{})
	go func() { _, _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(0x00100000, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	event, err := syscall.WaitForSingleObject(handle, 0)
	return err == nil && event == 258
}
