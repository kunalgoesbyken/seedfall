package kv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	segPrefix  = "seg-"
	basePrefix = "base-"
	dataSuffix = ".log"
	tmpSuffix  = ".tmp"
	idDigits   = 20
)

type fileKind int

const (
	kindSeg fileKind = iota
	kindBase
	kindTmp
)

func segName(id uint64) string {
	return fmt.Sprintf("%s%0*d%s", segPrefix, idDigits, id, dataSuffix)
}

func baseName(id uint64) string {
	return fmt.Sprintf("%s%0*d%s", basePrefix, idDigits, id, dataSuffix)
}

func parseName(name string) (fileKind, uint64, bool) {
	if strings.HasSuffix(name, tmpSuffix) {
		return kindTmp, 0, true
	}
	for _, c := range []struct {
		prefix string
		kind   fileKind
	}{{segPrefix, kindSeg}, {basePrefix, kindBase}} {
		rest, ok := strings.CutPrefix(name, c.prefix)
		if !ok {
			continue
		}
		digits, ok := strings.CutSuffix(rest, dataSuffix)
		if !ok || len(digits) != idDigits {
			return 0, 0, false
		}
		id, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		return c.kind, id, true
	}
	return 0, 0, false
}

func ensureDir(dir string) error {
	_, err := os.Stat(dir)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dir))
}

func (db *DB) createFile(path string, magic [fileHeaderSize]byte) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteAt(magic[:], 0); err != nil {
		f.Close()
		return nil, err
	}
	if err := db.fsync(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
