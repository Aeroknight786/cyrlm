package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// unpack validates paths and links, stages a complete tree, then renames it.
func unpack(ctx context.Context, source, dest, root string) error {
	if info, err := os.Stat(dest); err == nil && info.IsDir() {
		return nil
	}
	stage := dest + ".install"
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var links []struct{ name, target string }
	write := func(name string, mode os.FileMode, r io.Reader) error {
		name = strings.ReplaceAll(name, "\\", "/")
		if root != "" {
			if name == root || name == root+"/" {
				return nil
			}
			if !strings.HasPrefix(name, root+"/") {
				return errors.New("unexpected archive root")
			}
			name = strings.TrimPrefix(name, root+"/")
		}
		destPath := filepath.Join(stage, filepath.FromSlash(name))
		if path.IsAbs(name) || !inside(stage, destPath) || strings.Contains(name, ":") {
			return errors.New("unsafe archive path")
		}
		if mode.IsDir() {
			return os.MkdirAll(destPath, 0700)
		}
		if mode&os.ModeSymlink != 0 {
			b, err := io.ReadAll(io.LimitReader(r, 4096))
			target := string(b)
			if err != nil || filepath.IsAbs(target) || !inside(stage, filepath.Join(filepath.Dir(destPath), target)) {
				return errors.New("unsafe archive symlink")
			}
			links = append(links, struct{ name, target string }{destPath, target})
			return nil
		}
		if !mode.IsRegular() {
			return errors.New("unsupported archive entry")
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()&0777)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		return errors.Join(err, f.Close())
	}
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 2)
	_, err = io.ReadFull(f, magic)
	if err != nil {
		return err
	}
	_, _ = f.Seek(0, 0)
	if string(magic) == "PK" {
		info, _ := f.Stat()
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return err
		}
		for _, entry := range zr.File {
			reader, err := entry.Open()
			if err != nil {
				return err
			}
			err = write(entry.Name, entry.Mode(), reader)
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				return errors.Join(err, closeErr)
			}
		}
	} else {
		var reader io.Reader
		var decoder *exec.Cmd
		var pipe io.ReadCloser
		waited := false
		if string(magic) == "\x28\xb5" {
			decoder = exec.CommandContext(ctx, "zstd", "-d", "-c", source)
			pipe, err = decoder.StdoutPipe()
			if err != nil {
				return err
			}
			if err = decoder.Start(); err != nil {
				return err
			}
			defer func() {
				_ = pipe.Close()
				if !waited {
					_ = decoder.Process.Kill()
					_ = decoder.Wait()
				}
			}()
			reader = pipe
		} else {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return err
			}
			defer gz.Close()
			reader = gz
		}
		tr := tar.NewReader(reader)
		for {
			entry, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			mode := entry.FileInfo().Mode()
			reader := io.Reader(tr)
			if mode&os.ModeSymlink != 0 {
				reader = strings.NewReader(entry.Linkname)
			}
			if err = write(entry.Name, mode, reader); err != nil {
				return err
			}
		}
		if decoder != nil {
			if _, err = io.Copy(io.Discard, reader); err != nil {
				return err
			}
			err = decoder.Wait()
			waited = true
			if err != nil {
				return err
			}
		}
	}
	for _, link := range links {
		if err := os.Symlink(link.target, link.name); err != nil {
			return err
		}
	}
	return os.Rename(stage, dest)
}
