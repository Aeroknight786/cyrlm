//go:build windows

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lock holds <setups>/<slug>.lock open with share mode 0: a second open fails
// until this process closes it or exits, and delete-on-close removes the file
// then. Locking the setup directory itself (as on Unix) would block the
// journal's atomic rename inside it on Windows.
func lock(path string) (func(), error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	name, err := syscall.UTF16PtrFromString(dir + ".lock")
	if err != nil {
		return nil, err
	}
	const deleteAccess, deleteOnClose = 0x00010000, 0x04000000
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE|deleteAccess, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL|deleteOnClose, 0)
	if err != nil {
		return nil, fmt.Errorf("setup journal is locked: %w", err)
	}
	return func() { _ = syscall.CloseHandle(handle) }, nil
}
