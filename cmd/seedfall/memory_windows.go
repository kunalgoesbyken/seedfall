package main

import (
	"syscall"
	"unsafe"
)

type memoryStatus struct {
	length, memoryLoad                                                       uint32
	totalPhys, availPhys, totalPage, availPage, totalVirt, availVirt, availX uint64
}

func freeMemory() (uint64, bool) {
	status := memoryStatus{length: uint32(unsafe.Sizeof(memoryStatus{}))}
	ok, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&status)))
	return status.availPhys, ok != 0
}
