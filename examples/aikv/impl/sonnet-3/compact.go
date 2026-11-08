package kv

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
)

type liveRef struct {
	key string
	e   entry
}

type snapshot struct {
	maxID  uint64
	sealed []*segment
	live   []liveRef
}

func (db *DB) Compact() error {
	return db.compact()
}

func (db *DB) compactLoop() {
	defer db.wg.Done()
	for {
		select {
		case <-db.stop:
			return
		case <-db.compactCh:
			if db.needsCompaction() {
				_ = db.compact()
			}
		}
	}
}

func (db *DB) compact() error {
	db.compactMu.Lock()
	defer db.compactMu.Unlock()
	if db.closed.Load() {
		return ErrClosed
	}
	snap, err := db.takeSnapshot()
	if err != nil || snap == nil {
		return err
	}
	merged, offs, err := db.writeMerged(snap)
	if err != nil {
		return err
	}
	return db.install(snap, merged, offs)
}

func (db *DB) takeSnapshot() (*snapshot, error) {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	if err := db.writableLocked(); err != nil {
		return nil, err
	}
	if db.active.size > segHeaderSize {
		if err := db.rotate(); err != nil {
			return nil, err
		}
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	var sealed []*segment
	for _, s := range db.segs {
		if s.id < db.active.id {
			sealed = append(sealed, s)
		}
	}
	if len(sealed) == 0 || (len(sealed) == 1 && sealed[0].cmp) {
		return nil, nil
	}
	sort.Slice(sealed, func(i, j int) bool { return sealed[i].id < sealed[j].id })
	maxID := sealed[len(sealed)-1].id
	var live []liveRef
	for k, e := range db.keys {
		if e.seg.id <= maxID {
			live = append(live, liveRef{key: k, e: e})
		}
	}
	sort.Slice(live, func(i, j int) bool {
		a, b := live[i].e, live[j].e
		if a.seg.id != b.seg.id {
			return a.seg.id < b.seg.id
		}
		return a.off < b.off
	})
	return &snapshot{maxID: maxID, sealed: sealed, live: live}, nil
}

func (db *DB) copyLive(out *os.File, live []liveRef) ([]int64, int64, error) {
	w := bufio.NewWriterSize(out, 1<<20)
	if _, err := w.Write(segMagic[:]); err != nil {
		return nil, 0, err
	}
	pos := int64(segHeaderSize)
	offs := make([]int64, len(live))
	var buf []byte
	for i, r := range live {
		if i&255 == 0 && db.closed.Load() {
			return nil, 0, ErrClosed
		}
		n := int(r.e.size)
		if cap(buf) < n {
			buf = make([]byte, n)
		}
		buf = buf[:n]
		if _, err := r.e.seg.f.ReadAt(buf, r.e.off); err != nil {
			return nil, 0, err
		}
		if _, ok := decodePut(buf, r.key); !ok {
			return nil, 0, ErrCorrupt
		}
		if _, err := w.Write(buf); err != nil {
			return nil, 0, err
		}
		offs[i] = pos
		pos += int64(n)
	}
	if err := w.Flush(); err != nil {
		return nil, 0, err
	}
	return offs, pos, nil
}

func (db *DB) writeMerged(snap *snapshot) (*segment, []int64, error) {
	final := filepath.Join(db.dir, segName(snap.maxID, true))
	tmp := final + tmpSuffix
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, err
	}
	offs, size, err := db.copyLive(out, snap.live)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, nil, err
	}
	db.callHook("tmp-synced")
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return nil, nil, err
	}
	db.callHook("renamed")
	if err := syncDir(db.dir); err != nil {
		return nil, nil, err
	}
	db.callHook("committed")
	f, err := os.Open(final)
	if err != nil {
		return nil, nil, err
	}
	return &segment{id: snap.maxID, cmp: true, f: f, size: size}, offs, nil
}

func (db *DB) install(snap *snapshot, merged *segment, offs []int64) error {
	db.writeMu.Lock()
	db.mu.Lock()
	if db.closed.Load() {
		db.mu.Unlock()
		db.writeMu.Unlock()
		_ = merged.f.Close()
		return ErrClosed
	}
	for i, r := range snap.live {
		if cur, ok := db.keys[r.key]; ok && cur == r.e {
			db.keys[r.key] = entry{seg: merged, off: offs[i], size: r.e.size}
		}
	}
	var replaced int64
	for _, s := range snap.sealed {
		delete(db.segs, s.id)
		replaced += s.size
	}
	db.segs[merged.id] = merged
	db.total += merged.size - replaced
	db.mu.Unlock()
	db.writeMu.Unlock()
	db.callHook("installed")

	var first error
	for _, s := range snap.sealed {
		if err := s.f.Close(); err != nil && first == nil {
			first = err
		}
		if err := os.Remove(filepath.Join(db.dir, segName(s.id, s.cmp))); err != nil && first == nil {
			first = err
		}
		db.callHook("removed")
	}
	if err := syncDir(db.dir); err != nil && first == nil {
		first = err
	}
	return first
}
