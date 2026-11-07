//go:build !unix && !windows

package download

func freeBytes(string) int64 { return -1 }
