package kv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

type options struct {
	maxSegmentSize int64
	compactMinSize int64
	noSync         bool
}

var defaultOptions = options{
	maxSegmentSize: 64 << 20,
	compactMinSize: 16 << 20,
}

type segment struct {
	id   uint64
	base bool
	f    *os.File
	size int64
}

func (s *segment) name() string {
	if s.base {
		return baseName(s.id)
	}
	return segName(s.id)
}

type location struct {
	seg  *segment
	off  int64
	size int64
}

type DB struct {
	dir  string
	opts options

	writeMu sync.Mutex
	queueMu sync.Mutex
	queue   []*writeReq

	mu         sync.RWMutex
	index      map[string]location
	files      []*segment
	active     *segment
	liveBytes  int64
	totalBytes int64
	closed     bool
	closing    bool
	fatal      error
	compacting bool

	compactWG sync.WaitGroup
	hook      func(stage string) error
}

var (
	openDirsMu sync.Mutex
	openDirs   = map[string]struct{}{}
)

func lockDir(dir string) error {
	openDirsMu.Lock()
	defer openDirsMu.Unlock()
	if _, ok := openDirs[dir]; ok {
		return fmt.Errorf("%w: %s", ErrLocked, dir)
	}
	openDirs[dir] = struct{}{}
	return nil
}

func unlockDir(dir string) {
	openDirsMu.Lock()
	defer openDirsMu.Unlock()
	delete(openDirs, dir)
}

func Open(dir string) (*DB, error) {
	return open(dir, defaultOptions)
}

func open(dir string, opts options) (*DB, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := lockDir(abs); err != nil {
		return nil, err
	}
	db := &DB{dir: abs, opts: opts, index: map[string]location{}}
	if err := db.recover(); err != nil {
		db.closeFiles()
		unlockDir(abs)
		return nil, err
	}
	return db, nil
}

func (db *DB) fsync(f *os.File) error {
	if db.opts.noSync {
		return nil
	}
	return f.Sync()
}

func (db *DB) path(name string) string {
	return filepath.Join(db.dir, name)
}

func (db *DB) recover() error {
	if err := ensureDir(db.dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(db.dir)
	if err != nil {
		return err
	}
	var segIDs, baseIDs []uint64
	var garbage []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		kind, id, ok := parseName(e.Name())
		if !ok {
			continue
		}
		switch kind {
		case kindSeg:
			segIDs = append(segIDs, id)
		case kindBase:
			baseIDs = append(baseIDs, id)
		case kindTmp:
			garbage = append(garbage, e.Name())
		}
	}
	slices.Sort(segIDs)
	slices.Sort(baseIDs)

	var baseID uint64
	if n := len(baseIDs); n > 0 {
		baseID = baseIDs[n-1]
		for _, id := range baseIDs[:n-1] {
			garbage = append(garbage, baseName(id))
		}
		if err := db.replayBase(baseID); err != nil {
			return err
		}
	}

	var live []uint64
	for _, id := range segIDs {
		if len(baseIDs) > 0 && id <= baseID {
			garbage = append(garbage, segName(id))
			continue
		}
		live = append(live, id)
	}
	for i, id := range live {
		if err := db.replaySegment(id, i == len(live)-1); err != nil {
			return err
		}
	}

	if len(garbage) > 0 {
		if err := db.removeFiles(garbage); err != nil {
			return err
		}
	}

	if len(live) == 0 {
		seg, err := db.createSegment(baseID + 1)
		if err != nil {
			return err
		}
		db.files = append(db.files, seg)
		db.active = seg
	} else {
		db.active = db.files[len(db.files)-1]
	}
	for _, s := range db.files {
		db.totalBytes += s.size
	}
	return nil
}

func (db *DB) replayFn(seg *segment) scanFunc {
	return func(typ byte, key string, off, size int64) {
		if old, ok := db.index[key]; ok {
			db.liveBytes -= old.size
		}
		if typ == recPut {
			db.index[key] = location{seg: seg, off: off, size: size}
			db.liveBytes += size
			return
		}
		delete(db.index, key)
	}
}

func (db *DB) openExisting(id uint64, base, writable bool) (*segment, error) {
	seg := &segment{id: id, base: base}
	flag := os.O_RDONLY
	if writable {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(db.path(seg.name()), flag, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	seg.f = f
	seg.size = st.Size()
	return seg, nil
}

func (db *DB) replayBase(id uint64) error {
	seg, err := db.openExisting(id, true, false)
	if err != nil {
		return err
	}
	db.files = append(db.files, seg)
	res, err := scanFile(seg.f, seg.size, baseMagic, db.replayFn(seg))
	if err != nil {
		return err
	}
	if res.badHeader || res.torn {
		return corruptf("invalid data in %s at offset %d", seg.name(), res.validEnd)
	}
	return nil
}

func (db *DB) replaySegment(id uint64, last bool) error {
	seg, err := db.openExisting(id, false, last)
	if err != nil {
		return err
	}
	db.files = append(db.files, seg)
	res, err := scanFile(seg.f, seg.size, segMagic, db.replayFn(seg))
	if err != nil {
		return err
	}
	if res.badHeader {
		return corruptf("invalid header in %s", seg.name())
	}
	if !last {
		if res.torn {
			return corruptf("invalid record in %s at offset %d", seg.name(), res.validEnd)
		}
		return nil
	}
	if res.torn {
		if err := seg.f.Truncate(res.validEnd); err != nil {
			return err
		}
		if res.validEnd < fileHeaderSize {
			if _, err := seg.f.WriteAt(segMagic[:], 0); err != nil {
				return err
			}
			res.validEnd = fileHeaderSize
		}
		seg.size = res.validEnd
	}
	return db.fsync(seg.f)
}

func (db *DB) createSegment(id uint64) (*segment, error) {
	seg := &segment{id: id}
	f, err := db.createFile(db.path(seg.name()), segMagic)
	if err != nil {
		return nil, err
	}
	if err := syncDir(db.dir); err != nil {
		f.Close()
		return nil, err
	}
	seg.f = f
	seg.size = fileHeaderSize
	return seg, nil
}

func (db *DB) removeFiles(names []string) error {
	if err := syncDir(db.dir); err != nil {
		return err
	}
	var firstErr error
	for _, name := range names {
		if err := os.Remove(db.path(name)); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	if err := syncDir(db.dir); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (db *DB) closeFiles() error {
	var firstErr error
	for _, s := range db.files {
		if err := s.f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	db.files = nil
	db.active = nil
	return firstErr
}

func validate(key string, value []byte) error {
	if len(key) > maxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > maxValueSize {
		return ErrValueTooLarge
	}
	return nil
}

func (db *DB) Put(key string, value []byte) error {
	if err := validate(key, value); err != nil {
		return err
	}
	return db.apply(recPut, key, value)
}

func (db *DB) Delete(key string) error {
	if err := validate(key, nil); err != nil {
		return err
	}
	return db.apply(recDel, key, nil)
}

func (db *DB) writableLocked() error {
	if db.closed {
		return ErrClosed
	}
	if db.fatal != nil {
		return fmt.Errorf("kv: database failed, reopen required: %w", db.fatal)
	}
	return nil
}

func (db *DB) writable() error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.writableLocked()
}

func (db *DB) fail(err error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.fatal == nil {
		db.fatal = err
	}
	return err
}

type writeReq struct {
	typ  byte
	key  string
	rec  []byte
	off  int64
	skip bool
	done chan error
}

func (db *DB) apply(typ byte, key string, value []byte) error {
	req := &writeReq{typ: typ, key: key, rec: encodeRecord(typ, key, value), done: make(chan error, 1)}
	db.queueMu.Lock()
	db.queue = append(db.queue, req)
	db.queueMu.Unlock()

	db.writeMu.Lock()
	select {
	case err := <-req.done:
		db.writeMu.Unlock()
		return err
	default:
	}
	db.queueMu.Lock()
	batch := db.queue
	db.queue = nil
	db.queueMu.Unlock()
	err := db.commit(batch)
	for _, r := range batch {
		r.done <- err
	}
	db.writeMu.Unlock()
	return <-req.done
}

func (db *DB) commit(batch []*writeReq) error {
	if err := db.writable(); err != nil {
		return err
	}
	var buf []byte
	exists := map[string]bool{}
	db.mu.RLock()
	for _, r := range batch {
		live, seen := exists[r.key]
		if !seen {
			_, live = db.index[r.key]
		}
		if r.typ == recDel && !live {
			r.skip = true
			continue
		}
		exists[r.key] = r.typ == recPut
		r.off = int64(len(buf))
		buf = append(buf, r.rec...)
	}
	db.mu.RUnlock()
	if len(buf) == 0 {
		return nil
	}

	size := int64(len(buf))
	if db.active.size > fileHeaderSize && db.active.size+size > db.opts.maxSegmentSize {
		if err := db.rotate(); err != nil {
			return err
		}
	}
	seg := db.active
	start := seg.size
	if _, err := seg.f.WriteAt(buf, start); err != nil {
		return db.fail(err)
	}
	if err := db.fsync(seg.f); err != nil {
		return db.fail(err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	seg.size += size
	db.totalBytes += size
	for _, r := range batch {
		if r.skip {
			continue
		}
		if old, ok := db.index[r.key]; ok {
			db.liveBytes -= old.size
		}
		if r.typ == recPut {
			recSize := int64(len(r.rec))
			db.index[r.key] = location{seg: seg, off: start + r.off, size: recSize}
			db.liveBytes += recSize
		} else {
			delete(db.index, r.key)
		}
	}
	db.maybeStartCompactionLocked()
	return nil
}

func (db *DB) rotate() error {
	seg, err := db.createSegment(db.active.id + 1)
	if err != nil {
		return db.fail(err)
	}
	db.mu.Lock()
	db.files = append(db.files, seg)
	db.active = seg
	db.totalBytes += seg.size
	db.mu.Unlock()
	return nil
}

func (db *DB) Get(key string) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, false, ErrClosed
	}
	loc, ok := db.index[key]
	if !ok {
		return nil, false, nil
	}
	buf := make([]byte, loc.size)
	if _, err := loc.seg.f.ReadAt(buf, loc.off); err != nil {
		return nil, false, err
	}
	typ, k, value, err := decodeRecord(buf)
	if err != nil || typ != recPut || k != key {
		return nil, false, corruptf("bad record for key in %s at offset %d", loc.seg.name(), loc.off)
	}
	return value, true, nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed || db.closing {
		db.mu.Unlock()
		return ErrClosed
	}
	db.closing = true
	db.mu.Unlock()

	db.compactWG.Wait()

	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	db.mu.Lock()
	defer db.mu.Unlock()
	db.closed = true
	var err error
	if db.fatal == nil {
		err = db.fsync(db.active.f)
	}
	if cerr := db.closeFiles(); err == nil {
		err = cerr
	}
	db.index = nil
	unlockDir(db.dir)
	return err
}
