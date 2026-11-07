//go:build !windows

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Lock the setup directory itself so a crash leaves no stale lock file.
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		directory.Close()
		return nil, fmt.Errorf("setup journal is locked: %w", err)
	}
	return func() { _ = syscall.Flock(int(directory.Fd()), syscall.LOCK_UN); _ = directory.Close() }, nil
}
