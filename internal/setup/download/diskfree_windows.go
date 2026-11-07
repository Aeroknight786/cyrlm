//go:build windows

package download

import (
	"syscall"
	"unsafe"
)

func freeBytes(path string) int64 {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return -1
	}
	var available uint64
	dll := syscall.NewLazyDLL("kernel32.dll")
	proc := dll.NewProc("GetDiskFreeSpaceExW")
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if ok == 0 {
		return -1
	}
	if available > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(available)
}
