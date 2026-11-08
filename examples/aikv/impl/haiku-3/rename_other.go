//go:build !windows

package kv

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
