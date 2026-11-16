package kv

import (
	"bufio"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

const compactBufSize = 1 << 20

type liveItem struct {
	key    string
	old    entry
	newOff int64
}

func (db *DB) begin() error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return ErrClosed
	}
	db.wg.Add(1)
	return nil
}

func (db *DB) Compact() error {
	if err := db.begin(); err != nil {
		return err
	}
	defer db.wg.Done()
	db.cmu.Lock()
	defer db.cmu.Unlock()
	return db.compactLocked()
}

func (db *DB) maybeCompact() {
	db.mu.RLock()
	garbage := db.total - db.live
	want := !db.closed &&
		garbage >= db.opts.minGarbage &&
		garbage >= db.live &&
		db.total >= db.retryAt
	db.mu.RUnlock()
	if !want || !db.cmu.TryLock() {
		return
	}
	if err := db.begin(); err != nil {
		db.cmu.Unlock()
		return
	}
	go func() {
		defer db.wg.Done()
		defer db.cmu.Unlock()
		if err := db.compactLocked(); err != nil && !errors.Is(err, ErrClosed) {
			db.mu.Lock()
			db.retryAt = db.total + db.opts.minGarbage
			db.mu.Unlock()
		}
	}()
}

func (db *DB) compactLocked() error {
	sources, items, baseID, err := db.seal()
	if err != nil {
		return err
	}
	db.fire("sealed")
	if err := db.writeBase(sources, items, baseID); err != nil {
		return err
	}
	return db.install(sources, items, baseID)
}

func (db *DB) seal() (map[uint64]*segment, []liveItem, uint64, error) {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if db.failed != nil {
		return nil, nil, 0, db.failed
	}
	db.mu.RLock()
	closed := db.closed
	db.mu.RUnlock()
	if closed {
		return nil, nil, 0, ErrClosed
	}
	baseID := db.nextID
	next, err := createLog(db.path, baseID+1)
	if err != nil {
		return nil, nil, 0, err
	}
	db.nextID = baseID + 2

	db.mu.Lock()
	defer db.mu.Unlock()
	sources := maps.Clone(db.segments)
	items := make([]liveItem, 0, len(db.index))
	for k, e := range db.index {
		items = append(items, liveItem{key: k, old: e})
	}
	db.segments[next.id] = next
	db.active = next
	db.total += next.size
	return sources, items, baseID, nil
}

func (db *DB) writeBase(sources map[uint64]*segment, items []liveItem, baseID uint64) (err error) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i].old, items[j].old
		if a.seg != b.seg {
			return a.seg < b.seg
		}
		return a.off < b.off
	})

	tmpPath := filepath.Join(db.path, segName(baseID, extTmp))
	basePath := filepath.Join(db.path, segName(baseID, extBase))
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			if renamed {
				os.Remove(basePath)
			}
		}
	}()

	bw := bufio.NewWriterSize(f, compactBufSize)
	if _, err := bw.WriteString(fileMagic); err != nil {
		return err
	}
	off := fileHeaderLen
	var buf []byte
	for i := range items {
		select {
		case <-db.done:
			return ErrClosed
		default:
		}
		it := &items[i]
		src := sources[it.old.seg]
		if src == nil {
			return fmt.Errorf("%w: missing segment %d", ErrCorrupt, it.old.seg)
		}
		buf = slices.Grow(buf[:0], int(it.old.size))[:it.old.size]
		if _, err := src.f.ReadAt(buf, it.old.off); err != nil {
			return err
		}
		typ, k, _, ok := parseRecord(buf)
		if !ok || typ != typePut || string(k) != it.key {
			return fmt.Errorf("%w: segment %d offset %d", ErrCorrupt, it.old.seg, it.old.off)
		}
		if _, err := bw.Write(buf); err != nil {
			return err
		}
		it.newOff = off
		off += int64(len(buf))
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if err := syncFile(f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	db.fire("tmp-written")
	if err := renameFile(tmpPath, basePath); err != nil {
		return err
	}
	renamed = true
	if err := syncDir(db.path); err != nil {
		return err
	}
	db.fire("renamed")
	return nil
}

func (db *DB) install(sources map[uint64]*segment, items []liveItem, baseID uint64) error {
	basePath := filepath.Join(db.path, segName(baseID, extBase))
	f, err := os.Open(basePath)
	if err != nil {
		os.Remove(basePath)
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		os.Remove(basePath)
		return err
	}
	base := &segment{id: baseID, ext: extBase, f: f, size: info.Size()}

	db.mu.Lock()
	for i := range items {
		it := &items[i]
		if cur, ok := db.index[it.key]; ok && cur == it.old {
			db.index[it.key] = entry{seg: baseID, off: it.newOff, size: it.old.size}
		}
	}
	for id, s := range sources {
		db.total -= s.size
		delete(db.segments, id)
	}
	db.total += base.size
	db.segments[baseID] = base
	db.mu.Unlock()
	db.fire("swapped")

	for _, id := range slices.Sorted(maps.Keys(sources)) {
		s := sources[id]
		s.f.Close()
		os.Remove(filepath.Join(db.path, segName(id, s.ext)))
		db.fire("removed")
	}
	return nil
}
