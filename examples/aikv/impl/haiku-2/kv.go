package kv

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

var (
	ErrClosed   = errors.New("kv: database is closed")
	ErrCorrupt  = errors.New("kv: corrupt data")
	ErrTooLarge = errors.New("kv: key or value too large")
)

type entry struct {
	off  int64
	size int64
}

type keyDir struct {
	entries map[string]entry
	live    int64
}

func newKeyDir() *keyDir {
	return &keyDir{entries: make(map[string]entry)}
}

func (k *keyDir) set(key string, e entry) {
	if old, ok := k.entries[key]; ok {
		k.live -= old.size
	}
	k.entries[key] = e
	k.live += e.size
}

func (k *keyDir) remove(key string) {
	if old, ok := k.entries[key]; ok {
		k.live -= old.size
		delete(k.entries, key)
	}
}

type DB struct {
	mu     sync.RWMutex
	dir    string
	gen    uint64
	file   *os.File
	size   int64
	keys   *keyDir
	err    error
	closed bool
}

func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	gens, err := listGenerations(dir)
	if err != nil {
		return nil, err
	}
	if len(gens) == 0 {
		return createFirstGeneration(dir)
	}
	return recoverDB(dir, gens)
}

func createFirstGeneration(dir string) (*DB, error) {
	db := &DB{dir: dir, gen: 1, keys: newKeyDir()}
	f, err := createFile(db.path(db.gen))
	if err != nil {
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		f.Close()
		return nil, err
	}
	db.file = f
	return db, nil
}

func recoverDB(dir string, gens []uint64) (*DB, error) {
	cur := gens[len(gens)-1]
	db := &DB{dir: dir, gen: cur}
	f, err := os.OpenFile(db.path(cur), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	keys, valid, err := replay(f, info.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	if valid < info.Size() {
		if err := truncateAndSync(f, valid); err != nil {
			f.Close()
			return nil, err
		}
	}
	if err := removeOlderGenerations(db, gens[:len(gens)-1]); err != nil {
		f.Close()
		return nil, err
	}
	db.file = f
	db.size = valid
	db.keys = keys
	return db, nil
}

func removeOlderGenerations(db *DB, stale []uint64) error {
	if len(stale) == 0 {
		return nil
	}
	for _, g := range stale {
		if err := os.Remove(db.path(g)); err != nil {
			return err
		}
	}
	return syncDir(db.dir)
}

func (db *DB) Put(key string, value []byte) error {
	if len(key) > maxKeySize || len(value) > maxValueSize {
		return ErrTooLarge
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.usable(); err != nil {
		return err
	}
	if err := db.appendRecord(record{op: opPut, key: key, value: value}); err != nil {
		return err
	}
	db.maybeCompact()
	return nil
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.usable(); err != nil {
		return nil, false, err
	}
	e, ok := db.keys.entries[key]
	if !ok {
		return nil, false, nil
	}
	rec, _, err := readRecord(db.file, e.off, e.off+e.size)
	if err != nil {
		return nil, false, asCorrupt(err, e.off)
	}
	if rec.op != opPut || rec.key != key {
		return nil, false, asCorrupt(errBadRecord, e.off)
	}
	return rec.value, true, nil
}

func (db *DB) Delete(key string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.usable(); err != nil {
		return err
	}
	if _, ok := db.keys.entries[key]; !ok {
		return nil
	}
	if err := db.appendRecord(record{op: opDelete, key: key}); err != nil {
		return err
	}
	db.maybeCompact()
	return nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	if db.file == nil {
		return nil
	}
	return db.file.Close()
}

func (db *DB) usable() error {
	if db.closed {
		return ErrClosed
	}
	if db.err != nil {
		return fmt.Errorf("kv: database unusable after earlier failure, reopen it: %w", db.err)
	}
	return nil
}

func (db *DB) fail(err error) {
	if db.err == nil {
		db.err = err
	}
}

func (db *DB) appendRecord(rec record) error {
	buf := encodeRecord(rec)
	if _, err := db.file.WriteAt(buf, db.size); err != nil {
		if terr := db.file.Truncate(db.size); terr != nil {
			db.fail(terr)
		}
		return err
	}
	if err := db.file.Sync(); err != nil {
		db.fail(err)
		return err
	}
	e := entry{off: db.size, size: int64(len(buf))}
	db.size += e.size
	if rec.op == opPut {
		db.keys.set(rec.key, e)
	} else {
		db.keys.remove(rec.key)
	}
	return nil
}

func (db *DB) maybeCompact() {
	if db.size < compactMinSize || db.size-db.keys.live < db.size/2 {
		return
	}
	if err := db.compact(); err != nil {
		db.fail(err)
	}
}

func (db *DB) path(gen uint64) string {
	return genPath(db.dir, gen)
}
