//go:build !unix && !windows && !wasip1

package kv

func syncDir(string) error { return nil }
