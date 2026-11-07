//go:build windows

package selfupdate

import (
	"fmt"
	"os"
)

func replace(from, to string) error {
	old := to + ".old"
	if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(to, old); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		if e := os.Rename(old, to); e != nil {
			return fmt.Errorf("update failed: %v; restore %s: %w", err, old, e)
		}
		return err
	}
	_ = os.Remove(old)
	return nil
}
