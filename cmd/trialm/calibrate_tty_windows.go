//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

func isCalibrationTTY(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
func calibrationKey(f *os.File) (byte, error) {
	h := syscall.Handle(f.Fd())
	var mode uint32
	if e := syscall.GetConsoleMode(h, &mode); e != nil {
		return 0, e
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")
	set := func(v uint32) error {
		ok, _, e := proc.Call(uintptr(h), uintptr(v))
		if ok == 0 {
			return fmt.Errorf("set terminal mode: %w", e)
		}
		return nil
	}
	if e := set(mode &^ (0x1 | 0x2 | 0x4)); e != nil {
		return 0, e
	}
	defer func() { _ = set(mode) }()
	var b [1]byte
	_, e := io.ReadFull(f, b[:])
	return b[0], e
}
