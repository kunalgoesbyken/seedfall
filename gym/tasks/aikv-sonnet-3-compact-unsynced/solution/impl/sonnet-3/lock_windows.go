//go:build windows

package kv

import (
	"errors"
	"syscall"
)

func lockFile(path string) (func() error, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, errSharingViolation) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() error { return syscall.CloseHandle(h) }, nil
}

const errSharingViolation = syscall.Errno(32)
