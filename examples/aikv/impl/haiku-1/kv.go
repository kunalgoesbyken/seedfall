package kv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	logName           = "kv.log"
	tmpName           = "kv.log.tmp"
	compactMinGarbage = 1 << 20
	replayBufferSize  = 1 << 20
	compactBufferSize = 1 << 20
)

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrBroken        = errors.New("kv: database is in a failed state, reopen it")
	ErrCorrupt       = errors.New("kv: log is corrupt")
	ErrEmptyKey      = errors.New("kv: empty key")
	ErrKeyTooLarge   = errors.New("kv: key too large")
	ErrValueTooLarge = errors.New("kv: value too large")
	ErrLocked        = errors.New("kv: directory is already open in this process")
)

type entry struct {
	off int64
	n   int64
}

type DB struct {
	mu     sync.RWMutex
	dir    string
	f      *os.File
	index  map[string]entry
	size   int64
	live   int64
	closed bool
	broken bool
}

var (
	openMu   sync.Mutex
	openDirs = map[string]struct{}{}
)

func Open(dir string) (*DB, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := acquire(abs); err != nil {
		return nil, err
	}
	db, err := openAt(abs)
	if err != nil {
		release(abs)
		return nil, err
	}
	return db, nil
}

func openAt(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(dir, tmpName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, logName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	db := &DB{dir: dir, f: f, index: map[string]entry{}}
	if err := db.recoverLog(); err != nil {
		f.Close()
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		f.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Put(key string, value []byte) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.usable(); err != nil {
		return err
	}
	rec := encodeRecord(opPut, key, value)
	off, err := db.append(rec)
	if err != nil {
		return err
	}
	db.setEntry(key, entry{off: off, n: int64(len(rec))})
	return db.compactIfNeeded()
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.usable(); err != nil {
		return nil, false, err
	}
	e, found := db.index[key]
	if !found {
		return nil, false, nil
	}
	rec := make([]byte, e.n)
	if _, err := db.f.ReadAt(rec, e.off); err != nil {
		return nil, false, err
	}
	h, ok := verifyRecord(rec)
	if !ok || h.op != opPut || recordKey(rec, h) != key {
		return nil, false, ErrCorrupt
	}
	return rec[headerSize+int(h.klen):], true, nil
}

func (db *DB) Delete(key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.usable(); err != nil {
		return err
	}
	if _, found := db.index[key]; !found {
		return nil
	}
	if _, err := db.append(encodeRecord(opDelete, key, nil)); err != nil {
		return err
	}
	db.removeEntry(key)
	return db.compactIfNeeded()
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	db.index = nil
	err := db.f.Close()
	release(db.dir)
	return err
}

func checkKey(key string) error {
	switch {
	case key == "":
		return ErrEmptyKey
	case len(key) > MaxKeySize:
		return ErrKeyTooLarge
	}
	return nil
}

func (db *DB) usable() error {
	if db.closed {
		return ErrClosed
	}
	if db.broken {
		return ErrBroken
	}
	return nil
}

func (db *DB) append(rec []byte) (int64, error) {
	off := db.size
	if _, err := db.f.WriteAt(rec, off); err != nil {
		return 0, db.rollback(off, err)
	}
	if err := db.f.Sync(); err != nil {
		return 0, db.rollback(off, err)
	}
	db.size += int64(len(rec))
	return off, nil
}

func (db *DB) rollback(off int64, cause error) error {
	if err := db.f.Truncate(off); err != nil {
		db.broken = true
		return fmt.Errorf("kv: write failed (%v) and rollback failed: %w", cause, err)
	}
	if err := db.f.Sync(); err != nil {
		db.broken = true
		return fmt.Errorf("kv: write failed (%v) and rollback sync failed: %w", cause, err)
	}
	return fmt.Errorf("kv: write failed: %w", cause)
}

func (db *DB) setEntry(key string, e entry) {
	if old, found := db.index[key]; found {
		db.live -= old.n
	}
	db.index[key] = e
	db.live += e.n
}

func (db *DB) removeEntry(key string) {
	if old, found := db.index[key]; found {
		db.live -= old.n
		delete(db.index, key)
	}
}

func acquire(dir string) error {
	openMu.Lock()
	defer openMu.Unlock()
	if _, found := openDirs[dir]; found {
		return ErrLocked
	}
	openDirs[dir] = struct{}{}
	return nil
}

func release(dir string) {
	openMu.Lock()
	defer openMu.Unlock()
	delete(openDirs, dir)
}

func (db *DB) recoverLog() error {
	info, err := db.f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	end, err := db.replay(size)
	if err != nil {
		return err
	}
	if end < size {
		if err := db.f.Truncate(end); err != nil {
			return err
		}
		if err := db.f.Sync(); err != nil {
			return err
		}
	}
	db.size = end
	return nil
}

func (db *DB) replay(size int64) (int64, error) {
	r := bufio.NewReaderSize(io.NewSectionReader(db.f, 0, size), replayBufferSize)
	var off int64
	for off < size {
		if size-off < headerSize {
			return off, nil
		}
		var hdr [headerSize]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return 0, err
		}
		h, ok := parseHeader(hdr[:])
		if !ok {
			return db.tailOrCorrupt(off, size)
		}
		n := h.recordLen()
		if off+n > size {
			return off, nil
		}
		rec := make([]byte, n)
		copy(rec, hdr[:])
		if _, err := io.ReadFull(r, rec[headerSize:]); err != nil {
			return 0, err
		}
		if _, valid := verifyRecord(rec); !valid {
			if off+n == size {
				return off, nil
			}
			return db.tailOrCorrupt(off, size)
		}
		e := entry{off: off, n: n}
		switch h.op {
		case opPut:
			db.setEntry(recordKey(rec, h), e)
		case opDelete:
			db.removeEntry(recordKey(rec, h))
		}
		off += n
	}
	return off, nil
}

func (db *DB) tailOrCorrupt(off, size int64) (int64, error) {
	zero, err := allZeros(db.f, off, size)
	if err != nil {
		return 0, err
	}
	if zero {
		return off, nil
	}
	return 0, ErrCorrupt
}

func allZeros(f *os.File, off, size int64) (bool, error) {
	buf := make([]byte, 64<<10)
	for off < size {
		n := min(int64(len(buf)), size-off)
		m, err := f.ReadAt(buf[:n], off)
		if m != int(n) {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return false, err
		}
		for _, b := range buf[:m] {
			if b != 0 {
				return false, nil
			}
		}
		off += int64(m)
	}
	return true, nil
}

func (db *DB) compactIfNeeded() error {
	garbage := db.size - db.live
	if garbage < compactMinGarbage || garbage < db.live {
		return nil
	}
	if err := db.compact(); err != nil {
		return fmt.Errorf("kv: write committed but compaction failed: %w", err)
	}
	return nil
}

func (db *DB) compact() error {
	logPath := filepath.Join(db.dir, logName)
	tmpPath := filepath.Join(db.dir, tmpName)

	tmp, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	discard := func(cause error) error {
		tmp.Close()
		os.Remove(tmpPath)
		return cause
	}

	keys := make([]string, 0, len(db.index))
	for k := range db.index {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return db.index[keys[i]].off < db.index[keys[j]].off
	})

	newIndex := make(map[string]entry, len(keys))
	w := bufio.NewWriterSize(tmp, compactBufferSize)
	var off int64
	for _, k := range keys {
		e := db.index[k]
		rec := make([]byte, e.n)
		if _, err := db.f.ReadAt(rec, e.off); err != nil {
			return discard(err)
		}
		if _, valid := verifyRecord(rec); !valid {
			return discard(ErrCorrupt)
		}
		if _, err := w.Write(rec); err != nil {
			return discard(err)
		}
		newIndex[k] = entry{off: off, n: e.n}
		off += e.n
	}
	if err := w.Flush(); err != nil {
		return discard(err)
	}
	if err := tmp.Sync(); err != nil {
		return discard(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := db.f.Close(); err != nil {
		db.broken = true
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, logPath); err != nil {
		os.Remove(tmpPath)
		if rerr := db.reopen(logPath); rerr != nil {
			return rerr
		}
		return err
	}
	if err := db.reopen(logPath); err != nil {
		return err
	}
	if err := syncDir(db.dir); err != nil {
		db.broken = true
		return err
	}
	db.index = newIndex
	db.size = off
	db.live = off
	return nil
}

func (db *DB) reopen(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		db.broken = true
		return err
	}
	db.f = f
	return nil
}
