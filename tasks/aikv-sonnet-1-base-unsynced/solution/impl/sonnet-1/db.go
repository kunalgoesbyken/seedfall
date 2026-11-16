package kv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	MaxKeySize   = 1 << 16
	MaxValueSize = 1 << 30

	defaultSegmentSize = 64 << 20
	defaultMinGarbage  = 8 << 20
)

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrCorrupt       = errors.New("kv: data is corrupt")
	ErrInUse         = errors.New("kv: database is already open in this process")
	ErrKeyTooLarge   = errors.New("kv: key is too large")
	ErrValueTooLarge = errors.New("kv: value is too large")
)

type options struct {
	segmentSize int64
	minGarbage  int64
	hook        func(stage string)
}

func defaultOptions() options {
	return options{segmentSize: defaultSegmentSize, minGarbage: defaultMinGarbage}
}

type segment struct {
	id   uint64
	ext  string
	f    *os.File
	size int64
}

type DB struct {
	path string
	opts options

	wmu    sync.Mutex
	nextID uint64
	failed error

	mu       sync.RWMutex
	segments map[uint64]*segment
	index    map[string]entry
	active   *segment
	live     int64
	total    int64
	retryAt  int64
	closed   bool

	cmu  sync.Mutex
	done chan struct{}
	wg   sync.WaitGroup
}

var syncFile = func(f *os.File) error { return f.Sync() }

var openDirs = struct {
	sync.Mutex
	paths map[string]struct{}
}{paths: make(map[string]struct{})}

func Open(dir string) (*DB, error) {
	return open(dir, defaultOptions())
}

func open(dir string, opts options) (*DB, error) {
	path, err := resolveDir(dir)
	if err != nil {
		return nil, err
	}
	openDirs.Lock()
	if _, busy := openDirs.paths[path]; busy {
		openDirs.Unlock()
		return nil, ErrInUse
	}
	openDirs.paths[path] = struct{}{}
	openDirs.Unlock()

	db, err := load(path, opts)
	if err != nil {
		releaseDir(path)
		return nil, err
	}
	db.maybeCompact()
	return db, nil
}

func releaseDir(path string) {
	openDirs.Lock()
	delete(openDirs.paths, path)
	openDirs.Unlock()
}

func resolveDir(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("kv: empty directory name")
	}
	if err := mkdirAllDurable(dir); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func mkdirAllDurable(dir string) error {
	var missing []string
	for p := dir; ; {
		_, err := os.Stat(p)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, p := range missing {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(abs)); err != nil {
			return err
		}
	}
	return nil
}

type fileRef struct {
	id  uint64
	ext string
}

func listSegments(path string) (kept, superseded []fileRef, err error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, nil, err
	}
	var refs []fileRef
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ext, ok := parseSegName(e.Name())
		if !ok {
			continue
		}
		if ext == extTmp {
			if err := os.Remove(filepath.Join(path, e.Name())); err != nil {
				return nil, nil, err
			}
			continue
		}
		refs = append(refs, fileRef{id: id, ext: ext})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].id < refs[j].id })
	for i := 1; i < len(refs); i++ {
		if refs[i].id == refs[i-1].id {
			return nil, nil, fmt.Errorf("%w: duplicate segment id %d", ErrCorrupt, refs[i].id)
		}
	}
	var newest uint64
	hasBase := false
	for _, r := range refs {
		if r.ext == extBase {
			newest, hasBase = r.id, true
		}
	}
	for _, r := range refs {
		if hasBase && r.id < newest {
			superseded = append(superseded, r)
			continue
		}
		kept = append(kept, r)
	}
	return kept, superseded, nil
}

func load(path string, opts options) (*DB, error) {
	refs, superseded, err := listSegments(path)
	if err != nil {
		return nil, err
	}
	db := &DB{
		path:     path,
		opts:     opts,
		segments: make(map[uint64]*segment),
		index:    make(map[string]entry),
		done:     make(chan struct{}),
	}
	loaded := false
	defer func() {
		if !loaded {
			for _, s := range db.segments {
				s.f.Close()
			}
		}
	}()

	for i, ref := range refs {
		writable := i == len(refs)-1 && ref.ext == extLog
		seg, err := db.recoverSegment(ref, writable)
		if err != nil {
			return nil, err
		}
		db.segments[seg.id] = seg
		db.total += seg.size
		db.nextID = ref.id + 1
		if writable {
			db.active = seg
		}
	}
	if db.active == nil {
		seg, err := createLog(path, db.nextID)
		if err != nil {
			return nil, err
		}
		db.nextID++
		db.segments[seg.id] = seg
		db.total += seg.size
		db.active = seg
	}
	for _, r := range superseded {
		if err := os.Remove(filepath.Join(path, segName(r.id, r.ext))); err != nil {
			return nil, err
		}
	}
	if err := syncDir(path); err != nil {
		return nil, err
	}
	loaded = true
	return db, nil
}

func (db *DB) recoverSegment(ref fileRef, writable bool) (*segment, error) {
	name := segName(ref.id, ref.ext)
	flag := os.O_RDONLY
	if writable {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(filepath.Join(db.path, name), flag, 0)
	if err != nil {
		return nil, err
	}
	seg, err := db.recoverFile(f, ref.id, ref.ext, name, writable)
	if err != nil {
		f.Close()
		return nil, err
	}
	return seg, nil
}

func (db *DB) recoverFile(f *os.File, id uint64, ext, name string, writable bool) (*segment, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	valid, err := hasHeader(f, size)
	if err != nil {
		return nil, err
	}
	if !valid {
		if !writable || size > fileHeaderLen {
			return nil, fmt.Errorf("%w: %s: bad header", ErrCorrupt, name)
		}
		if err := resetFile(f, []byte(fileMagic)); err != nil {
			return nil, err
		}
		size = fileHeaderLen
	}
	end, err := scanRecords(f, size, db.replay(id))
	if err != nil {
		return nil, err
	}
	if end < size {
		if !writable {
			return nil, fmt.Errorf("%w: %s: invalid record at offset %d", ErrCorrupt, name, end)
		}
		if err := f.Truncate(end); err != nil {
			return nil, err
		}
		if err := syncFile(f); err != nil {
			return nil, err
		}
		size = end
	}
	return &segment{id: id, ext: ext, f: f, size: size}, nil
}

func resetFile(f *os.File, content []byte) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(content, 0); err != nil {
		return err
	}
	return syncFile(f)
}

func (db *DB) replay(id uint64) func(typ byte, key string, off, n int64) {
	return func(typ byte, key string, off, n int64) {
		if old, ok := db.index[key]; ok {
			db.live -= int64(old.size)
		}
		if typ == typePut {
			db.index[key] = entry{seg: id, off: off, size: uint32(n)}
			db.live += n
			return
		}
		delete(db.index, key)
	}
}

func createLog(dir string, id uint64) (*segment, error) {
	path := filepath.Join(dir, segName(id, extLog))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	abort := func(err error) (*segment, error) {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if _, err := f.WriteAt([]byte(fileMagic), 0); err != nil {
		return abort(err)
	}
	if err := syncFile(f); err != nil {
		return abort(err)
	}
	if err := syncDir(dir); err != nil {
		return abort(err)
	}
	return &segment{id: id, ext: extLog, f: f, size: fileHeaderLen}, nil
}

func (db *DB) fire(stage string) {
	if db.opts.hook != nil {
		db.opts.hook(stage)
	}
}

func (db *DB) Put(key string, value []byte) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	return db.write(typePut, key, value)
}

func (db *DB) Delete(key string) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}
	return db.write(typeDelete, key, nil)
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
	seg := db.segments[e.seg]
	if seg == nil {
		return nil, false, fmt.Errorf("%w: missing segment %d", ErrCorrupt, e.seg)
	}
	buf := make([]byte, e.size)
	if _, err := seg.f.ReadAt(buf, e.off); err != nil {
		return nil, false, err
	}
	typ, k, v, valid := parseRecord(buf)
	if !valid || typ != typePut || string(k) != key {
		return nil, false, fmt.Errorf("%w: segment %d offset %d", ErrCorrupt, e.seg, e.off)
	}
	return v, true, nil
}

func (db *DB) write(typ byte, key string, value []byte) error {
	rec := encodeRecord(typ, key, value)
	db.wmu.Lock()
	err := db.appendLocked(typ, key, rec)
	db.wmu.Unlock()
	if err != nil {
		return err
	}
	db.maybeCompact()
	return nil
}

func (db *DB) appendLocked(typ byte, key string, rec []byte) error {
	if db.failed != nil {
		return db.failed
	}
	db.mu.RLock()
	closed := db.closed
	_, exists := db.index[key]
	db.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	if typ == typeDelete && !exists {
		return nil
	}
	n := int64(len(rec))
	if db.active.size > fileHeaderLen && db.active.size+n > db.opts.segmentSize {
		if err := db.rollLocked(); err != nil {
			return err
		}
	}
	seg := db.active
	off := seg.size
	if _, err := seg.f.WriteAt(rec, off); err != nil {
		if terr := seg.f.Truncate(off); terr != nil {
			return db.fail(errors.Join(err, terr))
		}
		return err
	}
	db.fire("written")
	if err := syncFile(seg.f); err != nil {
		return db.fail(err)
	}
	seg.size += n

	db.mu.Lock()
	db.total += n
	if old, ok := db.index[key]; ok {
		db.live -= int64(old.size)
	}
	if typ == typePut {
		db.index[key] = entry{seg: seg.id, off: off, size: uint32(n)}
		db.live += n
	} else {
		delete(db.index, key)
	}
	db.mu.Unlock()
	return nil
}

func (db *DB) fail(err error) error {
	db.failed = fmt.Errorf("kv: store is unusable after I/O failure: %w", err)
	return db.failed
}

func (db *DB) rollLocked() error {
	seg, err := createLog(db.path, db.nextID)
	if err != nil {
		return err
	}
	db.nextID++
	db.mu.Lock()
	db.segments[seg.id] = seg
	db.active = seg
	db.total += seg.size
	db.mu.Unlock()
	return nil
}

func (db *DB) Close() error {
	db.wmu.Lock()
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		db.wmu.Unlock()
		return ErrClosed
	}
	db.closed = true
	db.mu.Unlock()
	db.wmu.Unlock()

	close(db.done)
	db.wg.Wait()

	db.mu.Lock()
	var err error
	for _, s := range db.segments {
		err = errors.Join(err, s.f.Close())
	}
	db.segments = nil
	db.index = nil
	db.mu.Unlock()
	releaseDir(db.path)
	return err
}
