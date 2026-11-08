//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package kv

import (
	"io"
	"os"
)

func lockFile(path string) (io.Closer, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}
