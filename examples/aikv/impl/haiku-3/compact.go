package kv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

func (db *DB) compactLocked() error {
	tmpPath := filepath.Join(db.dir, compactName)
	tmp, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	w := bufio.NewWriterSize(tmp, 1<<20)
	index := make(map[string]entry, len(db.index))
	var off int64
	for key, e := range db.index {
		rec, ok, err := readRecord(db.f, e.off, db.size)
		if err != nil {
			return err
		}
		if !ok || rec.op != opPut || string(rec.key) != key {
			return fmt.Errorf("%w: record at offset %d", ErrCorrupt, e.off)
		}
		buf := rec.encode()
		if _, err := w.Write(buf); err != nil {
			return err
		}
		index[key] = entry{off: off, size: int64(len(buf))}
		off += int64(len(buf))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	logPath := filepath.Join(db.dir, logName)
	db.f.Close()
	db.f = nil
	if err := replaceFile(tmpPath, logPath); err != nil {
		f, rerr := os.OpenFile(logPath, os.O_RDWR, 0o644)
		if rerr != nil {
			return db.fail(rerr)
		}
		db.f = f
		return err
	}
	renamed = true

	nf, err := os.OpenFile(logPath, os.O_RDWR, 0o644)
	if err != nil {
		return db.fail(err)
	}
	db.f = nf
	db.index = index
	db.size = off
	db.live = off

	if err := syncDir(db.dir); err != nil {
		return db.fail(err)
	}
	return nil
}
