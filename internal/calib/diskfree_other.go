//go:build !unix

package calib

// freeBytes is unknown on this platform; the run then relies on write errors.
func freeBytes(string) int64 { return -1 }
