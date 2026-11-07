//go:build !windows

package window

import "syscall"

func processAlive(pid int) bool { e := syscall.Kill(pid, 0); return e == nil || e == syscall.EPERM }
