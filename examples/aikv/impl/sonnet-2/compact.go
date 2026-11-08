package kv

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
)

type move struct {
	key    string
	oldID  uint64
	oldOff int64
	newOff int64
}

func (db *DB) Compact() error {
	db.compactMu.Lock()
	defer db.compactMu.Unlock()
	return db.compactLocked()
}

func (db *DB) autoCompact() {
	defer db.compacting.Store(false)
	db.compactMu.Lock()
	defer db.compactMu.Unlock()
	err := db.compactLocked()
	if errors.Is(err, ErrClosed) {
		return
	}
	db.mu.Lock()
	if err != nil {
		db.compactNotBefore = db.total + db.opts.CompactMinBytes
	} else {
		db.compactNotBefore = 0
	}
	db.mu.Unlock()
}

func (db *DB) compactLocked() error {
	if db.closed.Load() {
		return ErrClosed
	}
	inputs, cid, err := db.rollForCompaction()
	if err != nil || len(inputs) == 0 {
		return err
	}
	return db.compactInputs(inputs, cid)
}

func (db *DB) rollForCompaction() ([]*segment, uint64, error) {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	if db.closed.Load() {
		return nil, 0, ErrClosed
	}
	if db.failed != nil {
		return nil, 0, db.failed
	}

	db.mu.RLock()
	empty := db.total == 0
	inputs := make([]*segment, 0, len(db.segs))
	for _, s := range db.segs {
		inputs = append(inputs, s)
	}
	db.mu.RUnlock()
	if empty {
		return nil, 0, nil
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].id < inputs[j].id })

	cid := db.nextID
	db.nextID++
	seg, err := db.createSegment()
	if err != nil {
		return nil, 0, err
	}
	db.mu.Lock()
	db.segs[seg.id] = seg
	db.mu.Unlock()
	db.active = seg
	return inputs, cid, nil
}

func (db *DB) compactInputs(inputs []*segment, cid uint64) error {
	path := segPath(db.dir, cid, kindCmp)
	f, err := createFile(db.dir, cid, kindCmp)
	if err != nil {
		return err
	}
	durable := false
	defer func() {
		if !durable {
			f.Close()
			os.Remove(path)
		}
	}()
	db.hit("compact-created")

	w := bufio.NewWriterSize(f, 1<<20)
	var moves []move
	var outOff int64
	var buf []byte
	for _, in := range inputs {
		end, err := scan(in.f, in.size, func(r record) error {
			if db.closed.Load() {
				return ErrClosed
			}
			if r.kind != kindPut {
				return nil
			}
			db.mu.RLock()
			e, ok := db.index[string(r.key)]
			db.mu.RUnlock()
			if !ok || e.id != in.id || e.off != r.off {
				return nil
			}
			key := string(r.key)
			buf = encodeRecord(buf, kindPut, key, r.val)
			if _, err := w.Write(buf); err != nil {
				return err
			}
			moves = append(moves, move{key: key, oldID: in.id, oldOff: r.off, newOff: outOff})
			outOff += int64(len(buf))
			return nil
		})
		if err != nil {
			return err
		}
		if end != in.size {
			return fmt.Errorf("%w: %s is damaged at offset %d", ErrCorrupt, in.name(), end)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	db.hit("compact-data-synced")

	commit := encodeCommit(uint64(len(moves)), outOff)
	if _, err := f.WriteAt(commit, outOff); err != nil {
		return err
	}
	db.hit("compact-commit-written")
	if err := f.Sync(); err != nil {
		return err
	}
	if err := syncDir(db.dir); err != nil {
		return err
	}
	durable = true
	db.hit("compact-committed")

	out := &segment{id: cid, kind: kindCmp, f: f, size: outOff + int64(len(commit)), path: path}
	db.mu.Lock()
	for _, m := range moves {
		if e, ok := db.index[m.key]; ok && e.id == m.oldID && e.off == m.oldOff {
			e.id = cid
			e.off = m.newOff
			db.index[m.key] = e
		}
	}
	db.segs[cid] = out
	for _, in := range inputs {
		delete(db.segs, in.id)
		db.total -= in.size
	}
	db.total += out.size
	db.mu.Unlock()
	db.hit("compact-swapped")

	for _, in := range inputs {
		db.hit("compact-remove-input")
		in.f.Close()
		os.Remove(in.path)
	}
	return nil
}
