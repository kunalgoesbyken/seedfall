package kv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type dataFile struct {
	id  uint64
	cmp bool
}

func (d dataFile) name() string { return segName(d.id, d.cmp) }

func open(dir string, opts options) (*DB, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := mkdirAllSync(abs); err != nil {
		return nil, err
	}
	release, err := acquireDir(abs)
	if err != nil {
		return nil, err
	}
	db, err := recoverDB(abs, opts)
	if err != nil {
		release()
		return nil, err
	}
	db.release = release
	db.wg.Add(1)
	go db.compactLoop()
	db.maybeCompact()
	return db, nil
}

func mkdirAllSync(path string) error {
	fi, err := os.Stat(path)
	if err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("kv: %s is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if parent != path {
		if err := mkdirAllSync(parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return syncDir(parent)
}

func listDataFiles(dir string) ([]dataFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []dataFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if base, ok := strings.CutSuffix(name, tmpSuffix); ok {
			if _, _, valid := parseSegName(base); valid {
				if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return nil, err
				}
			}
			continue
		}
		if id, cmp, ok := parseSegName(name); ok {
			files = append(files, dataFile{id: id, cmp: cmp})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].id < files[j].id })
	return files, nil
}

func splitStale(files []dataFile) (live, stale []dataFile) {
	var base uint64
	var haveBase bool
	for _, f := range files {
		if f.cmp && (!haveBase || f.id > base) {
			base, haveBase = f.id, true
		}
	}
	for _, f := range files {
		if haveBase && (f.id < base || (f.id == base && !f.cmp)) {
			stale = append(stale, f)
		} else {
			live = append(live, f)
		}
	}
	return live, stale
}

func recoverDB(dir string, opts options) (*DB, error) {
	all, err := listDataFiles(dir)
	if err != nil {
		return nil, err
	}
	files, stale := splitStale(all)
	if len(stale) > 0 {
		if err := syncDir(dir); err != nil {
			return nil, err
		}
		for _, f := range stale {
			if err := os.Remove(filepath.Join(dir, f.name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
		}
	}

	db := &DB{
		dir:       dir,
		opts:      opts,
		keys:      make(map[string]entry),
		segs:      make(map[uint64]*segment),
		compactCh: make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}
	fail := func(err error) (*DB, error) {
		for _, s := range db.segs {
			_ = s.f.Close()
		}
		return nil, err
	}

	var maxID uint64
	for i, fi := range files {
		last := i == len(files)-1 && !fi.cmp
		seg, err := db.recoverSegment(fi, last)
		if err != nil {
			return fail(err)
		}
		db.segs[seg.id] = seg
		db.total += seg.size
		maxID = fi.id
	}

	if n := len(files); n > 0 && !files[n-1].cmp {
		db.active = db.segs[files[n-1].id]
		db.nextID = maxID + 1
	} else {
		id := maxID + 1
		seg, err := createSegment(dir, id)
		if err != nil {
			return fail(err)
		}
		db.segs[id] = seg
		db.active = seg
		db.total += seg.size
		db.nextID = id + 1
	}
	if err := syncDir(dir); err != nil {
		return fail(err)
	}
	return db, nil
}

func (db *DB) recoverSegment(fi dataFile, last bool) (*segment, error) {
	path := filepath.Join(db.dir, fi.name())
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	seg := &segment{id: fi.id, cmp: fi.cmp, f: f}
	fail := func(err error) (*segment, error) {
		_ = f.Close()
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	size := st.Size()

	headerOK := false
	if size >= segHeaderSize {
		var hdr [segHeaderSize]byte
		if _, err := f.ReadAt(hdr[:], 0); err != nil {
			return fail(err)
		}
		headerOK = hdr == segMagic
	}
	if !headerOK {
		if !last || size > segHeaderSize {
			return fail(fmt.Errorf("%w: bad header in %s", ErrCorrupt, fi.name()))
		}
		if err := f.Truncate(0); err != nil {
			return fail(err)
		}
		if _, err := f.WriteAt(segMagic[:], 0); err != nil {
			return fail(err)
		}
		if err := f.Sync(); err != nil {
			return fail(err)
		}
		size = segHeaderSize
	}

	end, clean, err := scanRecords(f, size, func(r scanned) {
		if old, ok := db.keys[r.key]; ok {
			db.live -= int64(old.size)
		}
		if r.typ == recPut {
			db.keys[r.key] = entry{seg: seg, off: r.off, size: uint32(r.size)}
			db.live += r.size
		} else {
			delete(db.keys, r.key)
		}
	})
	if err != nil {
		return fail(err)
	}
	if !clean {
		if !last {
			return fail(fmt.Errorf("%w: invalid record in %s at offset %d", ErrCorrupt, fi.name(), end))
		}
		if err := f.Truncate(end); err != nil {
			return fail(err)
		}
		if err := f.Sync(); err != nil {
			return fail(err)
		}
	}
	seg.size = end
	return seg, nil
}
