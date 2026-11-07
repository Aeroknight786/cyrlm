//go:build windows

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
	// Windows share mode zero provides a kernel-owned lock released on exit.
	name, err := syscall.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, fmt.Errorf("setup journal is locked: %w", err)
	}
	return func() { _ = syscall.CloseHandle(handle) }, nil
}
