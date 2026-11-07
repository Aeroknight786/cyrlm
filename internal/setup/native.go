package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Native model imports use an explicit triaLM namespace. Only files matching
// the approved HF digests can be removed from it during opt-in model undo.
func (e *Executor) undoNativeModels(j Journal, inv Inverse) error {
	if inv.NativeRepo != "trialm/"+j.Plan.Slug {
		return errors.New("invalid native model namespace")
	}
	home := e.Home
	for _, env := range inv.Env {
		if value, ok := strings.CutPrefix(env, "HOME="); ok {
			home = e.expand(value)
		}
	}
	nativeRoot := filepath.Join(home, ".lmstudio")
	pointer, err := os.ReadFile(filepath.Join(home, ".lmstudio-home-pointer"))
	if err == nil {
		nativeRoot = strings.TrimSpace(string(pointer))
		if !filepath.IsAbs(nativeRoot) {
			return errors.New("LM Studio home pointer must be absolute")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	namespace := filepath.Join(nativeRoot, "models", filepath.FromSlash(inv.NativeRepo))
	for ancestor := namespace; inside(nativeRoot, ancestor) && ancestor != nativeRoot; ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("native model namespace contains a symlink")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, step := range j.Plan.Steps {
		if step.Kind != Fetch || step.Request == nil {
			continue
		}
		file := filepath.Join(namespace, filepath.Base(step.Request.Dest))
		info, err := os.Lstat(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !matchesDigest(file, step.Request.SHA256) {
			return fmt.Errorf("native model changed; keeping %s", file)
		}
		if err = os.Remove(file); err != nil {
			return err
		}
	}
	// A native engine may keep additional metadata here. Empty directories can go.
	_ = os.Remove(namespace)
	_ = os.Remove(filepath.Dir(namespace))
	return nil
}
