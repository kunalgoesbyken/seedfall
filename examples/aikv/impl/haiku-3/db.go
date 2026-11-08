package kv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

const (
	logName     = "kv.log"
	compactName = "kv.log.compact"
)

var compactMinSize int64 = 1 << 20

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrCorrupt       = errors.New("kv: corrupt log")
	ErrEmptyKey      = errors.New("kv: empty key")
	ErrKeyTooLarge   = errors.New("kv: key too large")
	ErrValueTooLarge = errors.New("kv: value too large")
)

type entry struct {
	off  int64
	size int64
}

type DB struct {
	mu     sync.RWMutex
	dir    string
	f      *os.File
	index  map[string]entry
	size   int64
	live   int64
	closed bool
	failed error
}

func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(dir, compactName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, logName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	db := &DB{dir: dir, f: f, index: make(map[string]entry)}
	if err := db.recover(); err != nil {
		f.Close()
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		f.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) recover() error {
	info, err := db.f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	var off int64
	for off < size {
		rec, ok, err := readRecord(db.f, off, size)
		if err != nil {
			return err
		}
		if !ok {
			later, err := hasRecordAfter(db.f, off+1, size)
			if err != nil {
				return err
			}
			if later {
				return fmt.Errorf("%w: invalid record at offset %d followed by valid data", ErrCorrupt, off)
			}
			if err := db.f.Truncate(off); err != nil {
				return err
			}
			if err := db.f.Sync(); err != nil {
				return err
			}
			size = off
			break
		}
		db.apply(rec, off)
		off += rec.size()
	}
	db.size = size
	return nil
}

func (db *DB) apply(rec record, off int64) {
	key := string(rec.key)
	if old, ok := db.index[key]; ok {
		db.live -= old.size
	}
	if rec.op == opDelete {
		delete(db.index, key)
		return
	}
	db.index[key] = entry{off: off, size: rec.size()}
	db.live += rec.size()
}

func validate(key string, value []byte) error {
	if key == "" {
		return ErrEmptyKey
	}
	if len(key) > maxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > maxValueSize {
		return ErrValueTooLarge
	}
	return nil
}

func (db *DB) usable() error {
	if db.closed {
		return ErrClosed
	}
	return db.failed
}

func (db *DB) fail(err error) error {
	db.failed = fmt.Errorf("kv: database unusable after I/O failure, reopen to recover: %w", err)
	return db.failed
}

func (db *DB) Put(key string, value []byte) error {
	if err := validate(key, value); err != nil {
		return err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.usable(); err != nil {
		return err
	}
	return db.appendLocked(record{op: opPut, key: []byte(key), value: value})
}

func (db *DB) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.usable(); err != nil {
		return err
	}
	if _, ok := db.index[key]; !ok {
		return nil
	}
	return db.appendLocked(record{op: opDelete, key: []byte(key)})
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	if key == "" {
		return nil, false, ErrEmptyKey
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.usable(); err != nil {
		return nil, false, err
	}
	e, ok := db.index[key]
	if !ok {
		return nil, false, nil
	}
	rec, valid, err := readRecord(db.f, e.off, db.size)
	if err != nil {
		return nil, false, err
	}
	if !valid || rec.op != opPut || string(rec.key) != key {
		return nil, false, fmt.Errorf("%w: record at offset %d", ErrCorrupt, e.off)
	}
	return rec.value, true, nil
}

func (db *DB) appendLocked(rec record) error {
	buf := rec.encode()
	off := db.size
	if _, err := db.f.WriteAt(buf, off); err != nil {
		return db.fail(err)
	}
	if err := db.f.Sync(); err != nil {
		return db.fail(err)
	}
	db.size += int64(len(buf))
	db.apply(rec, off)
	if db.size >= compactMinSize && db.size >= 2*db.live {
		db.compactLocked()
	}
	return nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	if db.f == nil {
		return nil
	}
	return db.f.Close()
}
