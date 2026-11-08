package kv

import (
	"bufio"
	"cmp"
	"os"
	"slices"
)

type compactEntry struct {
	key    string
	oldLoc location
	newOff int64
}

func (db *DB) shouldCompactLocked() bool {
	return db.totalBytes >= db.opts.compactMinSize && db.liveBytes*2 < db.totalBytes
}

func (db *DB) maybeStartCompactionLocked() {
	if db.compacting || db.closing || db.closed || db.fatal != nil || !db.shouldCompactLocked() {
		return
	}
	db.compacting = true
	db.compactWG.Add(1)
	go func() {
		defer db.compactWG.Done()
		db.compact()
		db.mu.Lock()
		db.compacting = false
		db.mu.Unlock()
	}()
}

func (db *DB) runHook(stage string) error {
	if db.hook == nil {
		return nil
	}
	return db.hook(stage)
}

func (db *DB) compactNow() error {
	db.mu.Lock()
	if err := db.writableLocked(); err != nil {
		db.mu.Unlock()
		return err
	}
	if db.compacting || db.closing {
		db.mu.Unlock()
		return nil
	}
	db.compacting = true
	db.compactWG.Add(1)
	db.mu.Unlock()
	defer db.compactWG.Done()
	err := db.compact()
	db.mu.Lock()
	db.compacting = false
	db.mu.Unlock()
	return err
}

func (db *DB) freeze() (uint64, error) {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	if err := db.writable(); err != nil {
		return 0, err
	}
	if db.active.size > fileHeaderSize {
		if err := db.rotate(); err != nil {
			return 0, err
		}
	}
	return db.active.id - 1, nil
}

func (db *DB) compact() error {
	frozenMax, err := db.freeze()
	if err != nil {
		return err
	}
	if err := db.runHook("frozen"); err != nil {
		return err
	}

	db.mu.RLock()
	var olds []*segment
	for _, s := range db.files {
		if s.id <= frozenMax {
			olds = append(olds, s)
		}
	}
	var entries []compactEntry
	for k, loc := range db.index {
		if loc.seg.id <= frozenMax {
			entries = append(entries, compactEntry{key: k, oldLoc: loc})
		}
	}
	db.mu.RUnlock()

	if len(olds) == 0 || (len(olds) == 1 && olds[0].base) {
		return nil
	}
	slices.SortFunc(entries, func(a, b compactEntry) int {
		if c := cmp.Compare(a.oldLoc.seg.id, b.oldLoc.seg.id); c != 0 {
			return c
		}
		return cmp.Compare(a.oldLoc.off, b.oldLoc.off)
	})

	base := &segment{id: frozenMax, base: true}
	finalPath := db.path(base.name())
	tmpPath := finalPath + tmpSuffix
	size, err := db.writeBase(tmpPath, entries)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := db.runHook("written"); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := syncDir(db.dir); err != nil {
		return db.fail(err)
	}
	if err := db.runHook("renamed"); err != nil {
		return err
	}
	f, err := os.Open(finalPath)
	if err != nil {
		return db.fail(err)
	}
	base.f = f
	base.size = size

	db.mu.Lock()
	for _, e := range entries {
		if cur, ok := db.index[e.key]; ok && cur == e.oldLoc {
			db.index[e.key] = location{seg: base, off: e.newOff, size: e.oldLoc.size}
		}
	}
	kept := []*segment{base}
	for _, s := range db.files {
		if s.id > frozenMax {
			kept = append(kept, s)
			continue
		}
		db.totalBytes -= s.size
		s.f.Close()
	}
	db.files = kept
	db.totalBytes += base.size
	db.mu.Unlock()

	if err := db.runHook("swapped"); err != nil {
		return err
	}
	names := make([]string, len(olds))
	for i, s := range olds {
		names[i] = s.name()
	}
	return db.removeFiles(names)
}

func (db *DB) writeBase(path string, entries []compactEntry) (int64, error) {
	f, err := db.createFile(path, baseMagic)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(fileHeaderSize, 0); err != nil {
		return 0, err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	off := int64(fileHeaderSize)
	var buf []byte
	for i := range entries {
		loc := entries[i].oldLoc
		if int64(cap(buf)) < loc.size {
			buf = make([]byte, loc.size)
		}
		buf = buf[:loc.size]
		if _, err := loc.seg.f.ReadAt(buf, loc.off); err != nil {
			return 0, err
		}
		typ, key, _, err := decodeRecord(buf)
		if err != nil || typ != recPut || key != entries[i].key {
			return 0, corruptf("bad record in %s at offset %d", loc.seg.name(), loc.off)
		}
		if _, err := w.Write(buf); err != nil {
			return 0, err
		}
		entries[i].newOff = off
		off += loc.size
	}
	if err := w.Flush(); err != nil {
		return 0, err
	}
	if err := db.fsync(f); err != nil {
		return 0, err
	}
	return off, f.Close()
}
