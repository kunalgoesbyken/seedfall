package kv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

const (
	genPrefix = "gen-"
	genSuffix = ".log"
	tmpSuffix = ".tmp"
)

func genFileName(gen uint64) string {
	return fmt.Sprintf("%s%016d%s", genPrefix, gen, genSuffix)
}

func genPath(dir string, gen uint64) string {
	return filepath.Join(dir, genFileName(gen))
}

func parseGenName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, genPrefix) || !strings.HasSuffix(name, genSuffix) {
		return 0, false
	}
	gen, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(name, genPrefix), genSuffix), 10, 64)
	if err != nil || genFileName(gen) != name {
		return 0, false
	}
	return gen, true
}

func listGenerations(dir string) ([]uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var gens []uint64
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, tmpSuffix) {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return nil, err
			}
			continue
		}
		if gen, ok := parseGenName(name); ok {
			gens = append(gens, gen)
		}
	}
	slices.Sort(gens)
	return gens, nil
}

func replay(f *os.File, size int64) (*keyDir, int64, error) {
	kd := newKeyDir()
	var off int64
	for off < size {
		rec, n, err := readRecord(f, off, size)
		if errors.Is(err, errBadRecord) {
			found, serr := containsRecord(f, off+1, size)
			if serr != nil {
				return nil, 0, serr
			}
			if found {
				return nil, 0, fmt.Errorf("%w at offset %d: valid records follow damaged data", ErrCorrupt, off)
			}
			return kd, off, nil
		}
		if err != nil {
			return nil, 0, err
		}
		if rec.op == opPut {
			kd.set(rec.key, entry{off: off, size: n})
		} else {
			kd.remove(rec.key)
		}
		off += n
	}
	return kd, off, nil
}

func createFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
}

func truncateAndSync(f *os.File, size int64) error {
	if err := f.Truncate(size); err != nil {
		return err
	}
	return f.Sync()
}

func syncDir(dir string) error {
	switch runtime.GOOS {
	case "windows", "js":
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	return errors.Join(err, d.Close())
}

func asCorrupt(err error, off int64) error {
	if errors.Is(err, errBadRecord) {
		return fmt.Errorf("%w at offset %d", ErrCorrupt, off)
	}
	return err
}
