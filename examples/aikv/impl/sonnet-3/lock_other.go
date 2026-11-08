//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd) && !windows

package kv

func lockFile(string) (func() error, error) {
	return func() error { return nil }, nil
}
