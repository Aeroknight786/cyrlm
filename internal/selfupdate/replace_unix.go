//go:build !windows

package selfupdate

import "os"

func replace(from, to string) error { return os.Rename(from, to) }
