package kv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const (
	MaxKeySize   = 1 << 16
	MaxValueSize = 1 << 30

	defaultMaxSegmentSize  = 64 << 20
	defaultCompactMinBytes = 16 << 20
)

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrCorrupt       = errors.New("kv: data corruption detected")
	ErrKeyTooLarge   = errors.New("kv: key too large")
	ErrValueTooLarge = errors.New("kv: value too large")
	ErrAlreadyOpen   = errors.New("kv: directory is already open in this process")
)

type Options struct {
	MaxSegmentSize        int64
	CompactMinBytes       int64
	DisableAutoCompaction bool
}

func (o Options) withDefaults() Options {
	if o.MaxSegmentSize <= 0 {
		o.MaxSegmentSize = defaultMaxSegmentSize
	}
	if o.CompactMinBytes <= 0 {
		o.CompactMinBytes = defaultCompactMinBytes
	}
	return o
}

type entry struct {
	id   uint64
	off  int64
	size uint32
}

type DB struct {
	dir     string
	lockKey string
	opts    Options

	writeMu sync.Mutex
	active  *segment
	nextID  uint64
	failed  error

	mu               sync.RWMutex
	index            map[string]entry
	segs             map[uint64]*segment
	total            int64
	live             int64
	compactNotBefore int64

	closed     atomic.Bool
	compactMu  sync.Mutex
	compacting atomic.Bool

	hook func(point string)
}

var (
	registryMu sync.Mutex
	registry   = map[string]struct{}{}
)

func acquireDir(key string) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[key]; ok {
		return ErrAlreadyOpen
	}
	registry[key] = struct{}{}
	return nil
}

func releaseDir(key string) {
	registryMu.Lock()
	delete(registry, key)
	registryMu.Unlock()
}

func canonicalDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func Open(dir string) (*DB, error) {
	return OpenWithOptions(dir, Options{})
}

func OpenWithOptions(dir string, opts Options) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	key, err := canonicalDir(dir)
	if err != nil {
		return nil, err
	}
	if err := acquireDir(key); err != nil {
		return nil, err
	}
	db := &DB{
		dir:     key,
		lockKey: key,
		opts:    opts.withDefaults(),
		index:   make(map[string]entry),
		segs:    make(map[uint64]*segment),
	}
	if err := db.recover(); err != nil {
		db.closeFiles()
		releaseDir(key)
		return nil, err
	}
	return db, nil
}

func (db *DB) hit(point string) {
	if db.hook != nil {
		db.hook(point)
	}
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed.Load() {
		return nil, false, ErrClosed
	}
	e, ok := db.index[key]
	if !ok {
		return nil, false, nil
	}
	seg := db.segs[e.id]
	if seg == nil {
		return nil, false, fmt.Errorf("%w: segment %d missing", ErrCorrupt, e.id)
	}
	buf := make([]byte, e.size)
	if _, err := seg.f.ReadAt(buf, e.off); err != nil {
		return nil, false, err
	}
	rec, valid := decodeRecord(buf)
	if !valid || rec.kind != kindPut || string(rec.key) != key {
		return nil, false, fmt.Errorf("%w: bad record in %s at offset %d", ErrCorrupt, seg.name(), e.off)
	}
	return rec.val, true, nil
}

func (db *DB) Put(key string, value []byte) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	return db.apply(kindPut, key, value)
}

func (db *DB) Delete(key string) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}
	return db.apply(kindDelete, key, nil)
}

func (db *DB) apply(kind byte, key string, value []byte) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	if db.closed.Load() {
		return ErrClosed
	}
	if db.failed != nil {
		return db.failed
	}
	if kind == kindDelete {
		db.mu.RLock()
		_, exists := db.index[key]
		db.mu.RUnlock()
		if !exists {
			return nil
		}
	}
	rec := encodeRecord(nil, kind, key, value)
	if err := db.ensureRoom(int64(len(rec))); err != nil {
		return err
	}
	seg := db.active
	off := seg.size
	if _, err := seg.f.WriteAt(rec, off); err != nil {
		if terr := seg.f.Truncate(off); terr != nil {
			db.failed = fmt.Errorf("kv: write failed and could not be rolled back: %w", errors.Join(err, terr))
		}
		return err
	}
	db.hit("write-appended")
	if err := seg.f.Sync(); err != nil {
		db.failed = fmt.Errorf("kv: sync failed, database is read-only until reopened: %w", err)
		return db.failed
	}
	db.hit("write-synced")

	n := int64(len(rec))
	db.mu.Lock()
	seg.size += n
	db.total += n
	old, existed := db.index[key]
	if existed {
		db.live -= int64(old.size)
	}
	if kind == kindPut {
		db.index[key] = entry{id: seg.id, off: off, size: uint32(n)}
		db.live += n
	} else {
		delete(db.index, key)
	}
	trigger := db.shouldCompactLocked()
	db.mu.Unlock()

	if trigger && db.compacting.CompareAndSwap(false, true) {
		go db.autoCompact()
	}
	return nil
}

func (db *DB) shouldCompactLocked() bool {
	if db.opts.DisableAutoCompaction {
		return false
	}
	return db.total >= db.opts.CompactMinBytes &&
		db.total >= db.compactNotBefore &&
		(db.total-db.live)*2 >= db.total
}

func (db *DB) ensureRoom(n int64) error {
	if db.active.size == 0 || db.active.size+n <= db.opts.MaxSegmentSize {
		return nil
	}
	seg, err := db.createSegment()
	if err != nil {
		return err
	}
	db.mu.Lock()
	db.segs[seg.id] = seg
	db.mu.Unlock()
	db.active = seg
	return nil
}

func (db *DB) createSegment() (*segment, error) {
	id := db.nextID
	db.nextID++
	f, err := createFile(db.dir, id, kindSeg)
	if err != nil {
		return nil, err
	}
	if err := syncDir(db.dir); err != nil {
		f.Close()
		os.Remove(segPath(db.dir, id, kindSeg))
		return nil, err
	}
	return &segment{id: id, kind: kindSeg, f: f, path: segPath(db.dir, id, kindSeg)}, nil
}

func (db *DB) Close() error {
	if !db.closed.CompareAndSwap(false, true) {
		return nil
	}
	db.compactMu.Lock()
	defer db.compactMu.Unlock()
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	db.mu.Lock()
	defer db.mu.Unlock()

	var errs []error
	if db.active != nil && db.failed == nil {
		if err := db.active.f.Sync(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := db.closeFiles(); err != nil {
		errs = append(errs, err)
	}
	db.index = nil
	releaseDir(db.lockKey)
	return errors.Join(errs...)
}

func (db *DB) closeFiles() error {
	var errs []error
	for _, s := range db.segs {
		if err := s.f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	db.segs = nil
	return errors.Join(errs...)
}
