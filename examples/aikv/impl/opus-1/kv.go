package kv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	dataExt               = ".kv"
	tmpExt                = ".tmp"
	genDigits             = 16
	defaultCompactMinSize = 4 << 20
)

type entry struct {
	offset int64
	size   int64
}

type DB struct {
	dir string

	wmu            sync.Mutex
	failed         error
	compactMinSize int64
	compactFloor   int64

	mu     sync.RWMutex
	f      *os.File
	gen    uint64
	size   int64
	live   int64
	index  map[string]entry
	closed bool
}

func Open(dir string) (*DB, error) {
	dir = filepath.Clean(dir)
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	db := &DB{dir: dir, compactMinSize: defaultCompactMinSize}
	if err := db.recover(); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) Put(key string, value []byte) error {
	if err := checkSizes(key, value); err != nil {
		return err
	}
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if err := db.writable(); err != nil {
		return err
	}
	return db.appendLocked(opPut, key, value)
}

func (db *DB) Delete(key string) error {
	if err := checkSizes(key, nil); err != nil {
		return err
	}
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if err := db.writable(); err != nil {
		return err
	}
	if _, ok := db.index[key]; !ok {
		return nil
	}
	return db.appendLocked(opDel, key, nil)
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, false, ErrClosed
	}
	e, ok := db.index[key]
	if !ok {
		return nil, false, nil
	}
	value, err := readValue(db.f, key, e)
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (db *DB) Compact() error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if err := db.writable(); err != nil {
		return err
	}
	return db.compactLocked()
}

func (db *DB) Close() error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	db.closed = true
	db.index = nil
	err := db.f.Close()
	db.f = nil
	return err
}

func (db *DB) writable() error {
	if db.closed {
		return ErrClosed
	}
	return db.failed
}

func (db *DB) fail(err error) error {
	db.failed = fmt.Errorf("kv: database unusable after write failure: %w", err)
	return db.failed
}

func (db *DB) appendLocked(op byte, key string, value []byte) error {
	rec := encodeRecord(op, key, value)
	off := db.size
	if _, err := db.f.WriteAt(rec, off); err != nil {
		return db.fail(err)
	}
	if err := db.f.Sync(); err != nil {
		return db.fail(err)
	}
	db.mu.Lock()
	db.size = off + int64(len(rec))
	db.applyLocked(op, key, entry{offset: off, size: int64(len(rec))})
	db.mu.Unlock()
	if db.shouldCompact() {
		if err := db.compactLocked(); err != nil && db.failed == nil {
			db.compactFloor = db.size * 2
		}
	}
	return nil
}

func (db *DB) applyLocked(op byte, key string, e entry) {
	if old, ok := db.index[key]; ok {
		db.live -= old.size
		delete(db.index, key)
	}
	if op == opPut {
		db.index[key] = e
		db.live += e.size
	}
}

func (db *DB) shouldCompact() bool {
	if db.size < db.compactMinSize || db.size < db.compactFloor {
		return false
	}
	dead := db.size - fileHeaderSize - db.live
	return dead > db.live
}

func readValue(f *os.File, key string, e entry) ([]byte, error) {
	buf := make([]byte, e.size)
	if _, err := f.ReadAt(buf, e.offset); err != nil {
		return nil, err
	}
	op, k, value, err := decodeRecord(buf)
	if err != nil || op != opPut || k != key {
		return nil, fmt.Errorf("%w: record at offset %d", ErrCorrupt, e.offset)
	}
	return value, nil
}

func dataName(gen uint64) string {
	return fmt.Sprintf("%0*x%s", genDigits, gen, dataExt)
}

func parseDataName(name string) (uint64, bool) {
	base, ok := strings.CutSuffix(name, dataExt)
	if !ok || len(base) != genDigits {
		return 0, false
	}
	gen, err := strconv.ParseUint(base, 16, 64)
	return gen, err == nil && gen > 0
}

func (db *DB) recover() error {
	entries, err := os.ReadDir(db.dir)
	if err != nil {
		return err
	}
	var gens []uint64
	var tmps []string
	for _, de := range entries {
		name := de.Name()
		if strings.HasSuffix(name, dataExt+tmpExt) {
			tmps = append(tmps, name)
			continue
		}
		if gen, ok := parseDataName(name); ok && de.Type().IsRegular() {
			gens = append(gens, gen)
		}
	}
	var latest uint64
	for _, g := range gens {
		latest = max(latest, g)
	}
	if latest == 0 {
		if err := db.writeSnapshot(1, nil); err != nil {
			return err
		}
		latest = 1
	}
	if err := db.load(latest); err != nil {
		return err
	}
	removed := false
	for _, name := range tmps {
		if err := os.Remove(filepath.Join(db.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.f.Close()
			return err
		}
		removed = true
	}
	for _, g := range gens {
		if g == latest {
			continue
		}
		if err := os.Remove(filepath.Join(db.dir, dataName(g))); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.f.Close()
			return err
		}
		removed = true
	}
	if removed {
		if err := syncDir(db.dir); err != nil {
			db.f.Close()
			return err
		}
	}
	return nil
}

func (db *DB) load(gen uint64) (err error) {
	path := filepath.Join(db.dir, dataName(gen))
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	fileSize := fi.Size()
	hb := make([]byte, fileHeaderSize)
	if _, err := f.ReadAt(hb, 0); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: %s: truncated file header", ErrCorrupt, path)
		}
		return err
	}
	hdr, err := decodeFileHeader(hb)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if hdr.gen != gen || hdr.baseEnd > fileSize {
		return fmt.Errorf("%w: %s: header does not match file", ErrCorrupt, path)
	}
	index := make(map[string]entry)
	var live int64
	r := bufio.NewReaderSize(io.NewSectionReader(f, fileHeaderSize, fileSize-fileHeaderSize), 1<<20)
	off := int64(fileHeaderSize)
	for off < fileSize {
		rec, err := scanRecord(r, off, fileSize)
		if errors.Is(err, errBadRecord) {
			break
		}
		if err != nil {
			return err
		}
		if old, ok := index[rec.key]; ok {
			live -= old.size
			delete(index, rec.key)
		}
		if rec.op == opPut {
			index[rec.key] = entry{offset: off, size: rec.size}
			live += rec.size
		}
		off += rec.size
	}
	if off < hdr.baseEnd {
		return fmt.Errorf("%w: %s: invalid record at offset %d inside compacted region", ErrCorrupt, path, off)
	}
	if off < fileSize {
		if err := f.Truncate(off); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
	}
	db.f, db.gen, db.size, db.live, db.index = f, gen, off, live, index
	return nil
}

func (db *DB) writeSnapshot(gen uint64, src *os.File) (err error) {
	final := filepath.Join(db.dir, dataName(gen))
	tmp := final + tmpExt
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.Write(make([]byte, fileHeaderSize)); err != nil {
		return err
	}
	off := int64(fileHeaderSize)
	if src != nil {
		for key, e := range db.index {
			value, err := readValue(src, key, e)
			if err != nil {
				return err
			}
			rec := encodeRecord(opPut, key, value)
			if _, err := w.Write(rec); err != nil {
				return err
			}
			off += int64(len(rec))
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if _, err := f.WriteAt(encodeFileHeader(fileHeader{gen: gen, baseEnd: off}), 0); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := syncDir(db.dir); err != nil {
		return db.fail(err)
	}
	return nil
}

func (db *DB) compactLocked() error {
	newGen := db.gen + 1
	if err := db.writeSnapshot(newGen, db.f); err != nil {
		return err
	}
	newDB := &DB{dir: db.dir}
	if err := newDB.load(newGen); err != nil {
		return db.fail(err)
	}
	db.mu.Lock()
	old, oldGen := db.f, db.gen
	db.f, db.gen, db.size, db.live, db.index = newDB.f, newDB.gen, newDB.size, newDB.live, newDB.index
	db.compactFloor = 0
	db.mu.Unlock()
	old.Close()
	if err := os.Remove(filepath.Join(db.dir, dataName(oldGen))); err != nil {
		return nil
	}
	syncDir(db.dir)
	return nil
}
