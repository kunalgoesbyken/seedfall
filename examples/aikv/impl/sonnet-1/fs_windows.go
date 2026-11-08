//go:build windows

package kv

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var procMoveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func syncDir(string) error {
	return nil
}

func renameFile(from, to string) error {
	from16, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	to16, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	r, _, errno := procMoveFileEx.Call(
		uintptr(unsafe.Pointer(from16)),
		uintptr(unsafe.Pointer(to16)),
		uintptr(moveFileReplaceExisting|moveFileWriteThrough),
	)
	if r == 0 {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errno}
	}
	return nil
}
