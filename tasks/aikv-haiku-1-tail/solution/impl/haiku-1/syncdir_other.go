//go:build !unix && !wasip1

package kv

func syncDir(dir string) error {
	return nil
}
