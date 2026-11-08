package main

import (
	"errors"
	"log"
	"syscall"
)

const fileSize = 4096

func must(err error, what string) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}

func openFile() int {
	fd, err := syscall.Open("file", syscall.O_CREAT|syscall.O_RDWR, 0o644)
	must(err, "open")
	return fd
}

func main() {
	first, second := openFile(), openFile()
	must(syscall.Flock(first, syscall.LOCK_EX), "lock")
	if err := syscall.Flock(second, syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EAGAIN) {
		log.Fatalf("second lock = %v, want EAGAIN", err)
	}
	must(syscall.Close(first), "close the holder")
	must(syscall.Flock(second, syscall.LOCK_EX|syscall.LOCK_NB), "lock after the holder closed")

	_, err := syscall.Pwrite(second, make([]byte, fileSize), 0)
	must(err, "pwrite")
	mapped, err := syscall.Mmap(second, 0, fileSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	must(err, "mmap")
	mapped[0] = 'm'
	must(syscall.Fsync(second), "fsync")
	got := make([]byte, 1)
	_, err = syscall.Pread(second, got, 0)
	must(err, "pread")
	if got[0] != 'm' {
		log.Fatalf("pread returned %q, want the mapped write", got[0])
	}
	must(syscall.Munmap(mapped), "munmap")
}
