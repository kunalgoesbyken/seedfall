//go:build windows || wasip1

package kv

import (
	"os"
	"runtime"
)

func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	d.Sync()
	d.Close()
	return nil
}
