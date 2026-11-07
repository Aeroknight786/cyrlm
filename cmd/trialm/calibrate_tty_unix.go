//go:build !windows

package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// isCalibrationTTY checks the actual terminal ioctl through the OS's stty,
// avoiding treating /dev/null (also a character device) as an interactive TTY.
func isCalibrationTTY(f *os.File) bool {
	st, e := f.Stat()
	if e != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = f
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

// Read one key with the terminal mode restored before any benchmark starts.
func calibrationKey(f *os.File) (byte, error) {
	get := exec.Command("stty", "-g")
	get.Stdin = f
	old, e := get.Output()
	if e != nil {
		return 0, e
	}
	set := func(args ...string) error { cmd := exec.Command("stty", args...); cmd.Stdin = f; return cmd.Run() }
	if e = set("-icanon", "-echo", "-isig", "min", "1", "time", "0"); e != nil {
		return 0, e
	}
	defer func() { _ = set(strings.TrimSpace(string(old))) }()
	var b [1]byte
	_, e = io.ReadFull(f, b[:])
	return b[0], e
}
