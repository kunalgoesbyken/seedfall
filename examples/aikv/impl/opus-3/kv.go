package kv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrCorrupt       = errors.New("kv: data corruption detected")
	ErrLocked        = errors.New("kv: database directory is already in use")
	ErrKeyTooLarge   = errors.New("kv: key too large")
	ErrValueTooLarge = errors.New("kv: value too large")
)

const defaultCompactMinDead = 16 << 20

type entry struct {
	file uint64
	off  int64
	size uint32
}

type DB struct {
	mu sync.RWMutex

	dir  string
	lock io.Closer

	files      map[uint64]*os.File
	active     *os.File
	activeID   uint64
	activeSize int64

	index      map[string]entry
	liveBytes  int64
	totalBytes int64

	compactMinDead int64
	compactAt      int64

	err    error
	closed bool
}

func Open(dir string) (*DB, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	if !registerDir(abs) {
		return nil, fmt.Errorf("%w: %s", ErrLocked, abs)
	}
	lock, err := lockFile(filepath.Join(abs, lockName))
	if err != nil {
		unregisterDir(abs)
		return nil, err
	}
	db := &DB{
		dir:            abs,
		lock:           lock,
		files:          map[uint64]*os.File{},
		index:          map[string]entry{},
		compactMinDead: defaultCompactMinDead,
		compactAt:      defaultCompactMinDead,
	}
	if err := db.recover(); err != nil {
		db.release()
		return nil, err
	}
	return db, nil
}

func (db *DB) recover() error {
	files, tmps, err := listDir(db.dir)
	if err != nil {
		return err
	}
	for _, t := range tmps {
		if err := os.Remove(t); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	start := 0
	for i, f := range files {
		if f.kind == kindSnap {
			start = i
		}
	}
	live := files[start:]
	obsolete := files[:start]

	var maxID uint64
	for i, f := range live {
		if err := db.replay(f, i == len(live)-1); err != nil {
			return err
		}
		maxID = f.id
	}

	for _, f := range obsolete {
		if err := os.Remove(filepath.Join(db.dir, fileName(f.id, f.kind))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	if db.active != nil {
		return nil
	}
	return db.createActive(maxID + 1)
}

func (db *DB) replay(df dataFile, last bool) error {
	path := filepath.Join(db.dir, fileName(df.id, df.kind))
	writable := last && df.kind == kindLog
	flag := os.O_RDONLY
	if writable {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return err
	}
	db.files[df.id] = f
	st, err := f.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	tombstoneInSnap := false
	end, err := scanFile(f, size, func(r record) {
		if df.kind == kindSnap && r.kind != kindPut {
			tombstoneInSnap = true
		}
		db.apply(r, df.id)
	})
	if err != nil && !errors.Is(err, ErrCorrupt) {
		return err
	}
	if tombstoneInSnap {
		return fmt.Errorf("%w: unexpected tombstone in %s", ErrCorrupt, path)
	}
	db.totalBytes += end
	if end != size {
		if !writable {
			return fmt.Errorf("%w: invalid record at offset %d in %s", ErrCorrupt, end, path)
		}
		if err := f.Truncate(end); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if writable {
		db.active, db.activeID, db.activeSize = f, df.id, end
	}
	return nil
}

func (db *DB) apply(r record, file uint64) {
	if old, ok := db.index[r.key]; ok {
		db.liveBytes -= int64(old.size)
		delete(db.index, r.key)
	}
	if r.kind == kindPut {
		db.index[r.key] = entry{file: file, off: r.off, size: r.size}
		db.liveBytes += int64(r.size)
	}
}

func (db *DB) createActive(id uint64) error {
	path := filepath.Join(db.dir, fileName(id, kindLog))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := syncDir(db.dir); err != nil {
		f.Close()
		return err
	}
	db.files[id] = f
	db.active, db.activeID, db.activeSize = f, id, 0
	return nil
}

func (db *DB) Put(key string, value []byte) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	rec := encodeRecord(kindPut, key, value)

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.err != nil {
		return db.err
	}
	off, err := db.append(rec)
	if err != nil {
		return err
	}
	db.apply(record{kind: kindPut, key: key, off: off, size: uint32(len(rec))}, db.activeID)
	db.maybeCompact()
	return nil
}

func (db *DB) Delete(key string) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.err != nil {
		return db.err
	}
	if _, ok := db.index[key]; !ok {
		return nil
	}
	if _, err := db.append(encodeRecord(kindDelete, key, nil)); err != nil {
		return err
	}
	db.apply(record{kind: kindDelete, key: key}, db.activeID)
	db.maybeCompact()
	return nil
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.err != nil {
		return nil, false, db.err
	}
	e, ok := db.index[key]
	if !ok {
		return nil, false, nil
	}
	_, value, err := db.readRecord(e, key)
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (db *DB) readRecord(e entry, key string) ([]byte, []byte, error) {
	f := db.files[e.file]
	if f == nil {
		return nil, nil, fmt.Errorf("%w: missing file %d", ErrCorrupt, e.file)
	}
	buf := make([]byte, e.size)
	n, err := f.ReadAt(buf, e.off)
	if n < len(buf) {
		if err == nil || errors.Is(err, io.EOF) {
			err = ErrCorrupt
		}
		return nil, nil, err
	}
	kind, k, value, err := decodeRecord(buf)
	if err != nil {
		return nil, nil, err
	}
	if kind != kindPut || k != key {
		return nil, nil, fmt.Errorf("%w: index mismatch for key %q", ErrCorrupt, key)
	}
	return buf, value, nil
}

func (db *DB) append(rec []byte) (int64, error) {
	off := db.activeSize
	if _, err := db.active.WriteAt(rec, off); err != nil {
		if terr := db.active.Truncate(off); terr != nil {
			return 0, db.fail(err)
		}
		if serr := db.active.Sync(); serr != nil {
			return 0, db.fail(serr)
		}
		return 0, err
	}
	if err := db.active.Sync(); err != nil {
		return 0, db.fail(err)
	}
	db.activeSize += int64(len(rec))
	db.totalBytes += int64(len(rec))
	return off, nil
}

func (db *DB) fail(err error) error {
	if db.err == nil {
		db.err = fmt.Errorf("kv: database unusable after I/O failure, reopen to recover: %w", err)
	}
	return db.err
}

func (db *DB) maybeCompact() {
	dead := db.totalBytes - db.liveBytes
	if dead < db.compactAt || dead < db.liveBytes {
		return
	}
	if err := db.compact(); err != nil && db.err == nil {
		db.compactAt = 2 * dead
	}
}

func (db *DB) Compact() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.err != nil {
		return db.err
	}
	return db.compact()
}

func (db *DB) compact() error {
	snapID := db.activeID + 1
	snapPath := filepath.Join(db.dir, fileName(snapID, kindSnap))
	tmpPath := snapPath + tmpExt

	newIndex, size, err := db.writeSnapshot(tmpPath, snapID)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, snapPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := syncDir(db.dir); err != nil {
		return db.fail(err)
	}
	snap, err := os.Open(snapPath)
	if err != nil {
		return db.fail(err)
	}
	old := db.files
	db.files = map[uint64]*os.File{snapID: snap}
	if err := db.createActive(snapID + 1); err != nil {
		db.files = old
		db.files[snapID] = snap
		return db.fail(err)
	}
	for id, f := range old {
		f.Close()
		for _, kind := range []fileKind{kindLog, kindSnap} {
			os.Remove(filepath.Join(db.dir, fileName(id, kind)))
		}
	}
	db.index = newIndex
	db.liveBytes = size
	db.totalBytes = size
	db.compactAt = db.compactMinDead
	return nil
}

func (db *DB) writeSnapshot(path string, snapID uint64) (map[string]entry, int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	index := make(map[string]entry, len(db.index))
	var off int64
	for key, e := range db.index {
		rec, _, err := db.readRecord(e, key)
		if err != nil {
			return nil, 0, err
		}
		if _, err := w.Write(rec); err != nil {
			return nil, 0, err
		}
		index[key] = entry{file: snapID, off: off, size: e.size}
		off += int64(e.size)
	}
	if err := w.Flush(); err != nil {
		return nil, 0, err
	}
	if err := f.Sync(); err != nil {
		return nil, 0, err
	}
	if err := f.Close(); err != nil {
		return nil, 0, err
	}
	return index, off, nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	err := db.release()
	db.closed = true
	db.err = ErrClosed
	return err
}

func (db *DB) release() error {
	var first error
	for _, f := range db.files {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	db.files = nil
	db.active = nil
	if db.lock != nil {
		if err := db.lock.Close(); err != nil && first == nil {
			first = err
		}
		db.lock = nil
	}
	unregisterDir(db.dir)
	return first
}
