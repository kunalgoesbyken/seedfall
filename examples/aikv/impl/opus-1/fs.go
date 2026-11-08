package kv

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, errors.ErrUnsupported) || errors.Is(err, syscall.EINVAL) {
		return nil
	}
	return err
}

func ensureDir(dir string) error {
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return &os.PathError{Op: "open", Path: dir, Err: syscall.ENOTDIR}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := ensureDir(parent); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDir(parent)
}
