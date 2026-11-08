package kv

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
)

const (
	compactMinSize = 4 << 20
	copyBufSize    = 1 << 20
)

func (db *DB) compact() error {
	oldGen := db.gen
	next := oldGen + 1
	final := db.path(next)
	tmp := final + tmpSuffix

	keys, size, err := writeLive(db, tmp)
	if err != nil {
		return discard(tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return discard(tmp, err)
	}
	if err := syncDir(db.dir); err != nil {
		return err
	}

	f, err := os.OpenFile(final, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	old := db.file
	db.file = f
	db.gen = next
	db.size = size
	db.keys = keys
	_ = old.Close()
	_ = os.Remove(db.path(oldGen))
	return nil
}

func discard(path string, err error) error {
	if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return errors.Join(err, rerr)
	}
	return err
}

func writeLive(db *DB, path string) (*keyDir, int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, 0, err
	}
	keys, size, err := copyLive(db, f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, 0, err
	}
	return keys, size, nil
}

func copyLive(db *DB, f *os.File) (*keyDir, int64, error) {
	w := bufio.NewWriterSize(f, copyBufSize)
	keys := newKeyDir()
	var off int64
	for key, e := range db.keys.entries {
		rec, n, err := readRecord(db.file, e.off, e.off+e.size)
		if err != nil {
			return nil, 0, asCorrupt(err, e.off)
		}
		if rec.op != opPut || rec.key != key {
			return nil, 0, asCorrupt(errBadRecord, e.off)
		}
		if _, err := w.Write(encodeRecord(rec)); err != nil {
			return nil, 0, err
		}
		keys.set(key, entry{off: off, size: n})
		off += n
	}
	if err := w.Flush(); err != nil {
		return nil, 0, err
	}
	return keys, off, nil
}
