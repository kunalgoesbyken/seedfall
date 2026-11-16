package kv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrCorrupt       = errors.New("kv: data corruption detected")
	ErrLocked        = errors.New("kv: database directory is in use")
	ErrKeyTooLarge   = errors.New("kv: key too large")
	ErrValueTooLarge = errors.New("kv: value too large")
)

type options struct {
	maxSegmentSize    int64
	compactMinGarbage int64
}

var defaultOptions = options{
	maxSegmentSize:    64 << 20,
	compactMinGarbage: 8 << 20,
}

type segment struct {
	id   uint64
	cmp  bool
	f    *os.File
	size int64
}

type entry struct {
	seg  *segment
	off  int64
	size uint32
}

type DB struct {
	dir     string
	opts    options
	release func()
	hook    func(stage string)

	writeMu sync.Mutex
	failed  error
	nextID  uint64
	active  *segment

	mu    sync.RWMutex
	keys  map[string]entry
	segs  map[uint64]*segment
	total int64
	live  int64

	compactMu sync.Mutex
	compactCh chan struct{}
	stop      chan struct{}
	wg        sync.WaitGroup
	closed    atomic.Bool
}

func Open(dir string) (*DB, error) {
	return open(dir, defaultOptions)
}

func (db *DB) Put(key string, value []byte) error {
	if len(key) > maxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > maxValueSize {
		return ErrValueTooLarge
	}
	return db.write(recPut, key, value)
}

func (db *DB) Delete(key string) error {
	if len(key) > maxKeySize {
		return ErrKeyTooLarge
	}
	return db.write(recDelete, key, nil)
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed.Load() {
		return nil, false, ErrClosed
	}
	e, ok := db.keys[key]
	if !ok {
		return nil, false, nil
	}
	buf := make([]byte, e.size)
	if _, err := e.seg.f.ReadAt(buf, e.off); err != nil {
		return nil, false, err
	}
	value, ok := decodePut(buf, key)
	if !ok {
		return nil, false, fmt.Errorf("%w: record at %s offset %d", ErrCorrupt, segName(e.seg.id, e.seg.cmp), e.off)
	}
	return value, true, nil
}

func (db *DB) Close() error {
	if !db.closed.CompareAndSwap(false, true) {
		return ErrClosed
	}
	close(db.stop)
	db.wg.Wait()
	db.compactMu.Lock()
	defer db.compactMu.Unlock()
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	db.mu.Lock()
	defer db.mu.Unlock()
	var first error
	for _, s := range db.segs {
		if err := s.f.Close(); err != nil && first == nil {
			first = err
		}
	}
	db.segs = nil
	db.keys = nil
	db.release()
	return first
}

func (db *DB) writableLocked() error {
	if db.closed.Load() {
		return ErrClosed
	}
	if db.failed != nil {
		return fmt.Errorf("kv: store is read-only after failure: %w", db.failed)
	}
	return nil
}

func (db *DB) write(typ byte, key string, value []byte) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	if err := db.writableLocked(); err != nil {
		return err
	}
	if typ == recDelete {
		db.mu.RLock()
		_, exists := db.keys[key]
		db.mu.RUnlock()
		if !exists {
			return nil
		}
	}
	rec := encodeRecord(typ, key, value)
	if db.active.size > segHeaderSize && db.active.size+int64(len(rec)) > db.opts.maxSegmentSize {
		if err := db.rotate(); err != nil {
			return err
		}
	}
	seg := db.active
	off := seg.size
	if err := db.appendDurable(seg, rec); err != nil {
		return err
	}
	seg.size += int64(len(rec))
	size := uint32(len(rec))

	db.mu.Lock()
	if old, ok := db.keys[key]; ok {
		db.live -= int64(old.size)
	}
	if typ == recPut {
		db.keys[key] = entry{seg: seg, off: off, size: size}
		db.live += int64(size)
	} else {
		delete(db.keys, key)
	}
	db.total += int64(size)
	db.mu.Unlock()

	db.maybeCompact()
	return nil
}

func (db *DB) appendDurable(seg *segment, rec []byte) error {
	if _, err := seg.f.WriteAt(rec, seg.size); err != nil {
		if terr := seg.f.Truncate(seg.size); terr != nil {
			db.failed = errors.Join(err, terr)
		}
		return err
	}
	if err := seg.f.Sync(); err != nil {
		db.failed = err
		return err
	}
	return nil
}

func (db *DB) rotate() error {
	id := db.nextID
	seg, err := createSegment(db.dir, id)
	if err != nil {
		return err
	}
	db.nextID++
	db.mu.Lock()
	db.segs[id] = seg
	db.active = seg
	db.total += segHeaderSize
	db.mu.Unlock()
	return nil
}

func createSegment(dir string, id uint64) (*segment, error) {
	path := filepath.Join(dir, segName(id, false))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*segment, error) {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if _, err := f.WriteAt(segMagic[:], 0); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := syncDir(dir); err != nil {
		return fail(err)
	}
	return &segment{id: id, f: f, size: segHeaderSize}, nil
}

func (db *DB) needsCompaction() bool {
	db.mu.RLock()
	total, garbage := db.total, db.total-db.live
	db.mu.RUnlock()
	return garbage >= db.opts.compactMinGarbage && garbage*2 >= total
}

func (db *DB) maybeCompact() {
	if !db.needsCompaction() {
		return
	}
	select {
	case db.compactCh <- struct{}{}:
	default:
	}
}

func (db *DB) callHook(stage string) {
	if db.hook != nil {
		db.hook(stage)
	}
}
