//go:build !windows && !wasip1

package kv

import "os"

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

func renameFile(from, to string) error {
	return os.Rename(from, to)
}
