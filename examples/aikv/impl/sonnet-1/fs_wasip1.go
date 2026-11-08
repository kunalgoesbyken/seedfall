//go:build wasip1

package kv

import "os"

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return nil
	}
	_ = d.Sync()
	return d.Close()
}

func renameFile(from, to string) error {
	return os.Rename(from, to)
}
