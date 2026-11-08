//go:build wasip1

package kv

import (
	"errors"
	"os"
	"syscall"
)

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return nil
	}
	err = d.Sync()
	_ = d.Close()
	for _, unsupported := range []error{syscall.EINVAL, syscall.ENOTSUP, syscall.ENOSYS, syscall.EBADF} {
		if errors.Is(err, unsupported) {
			return nil
		}
	}
	return err
}
