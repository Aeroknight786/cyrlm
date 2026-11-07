//go:build unix

package calib

import "syscall"

// freeBytes is the space available to this user at path (-1 if unknown).
func freeBytes(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
