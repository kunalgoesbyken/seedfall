package kv

import (
	"fmt"
	"os"
)

func (db *DB) recover() error {
	files, err := listFiles(db.dir)
	if err != nil {
		return err
	}
	db.nextID = 1
	if len(files) > 0 {
		db.nextID = files[len(files)-1].id + 1
	}

	cut, err := db.findCommittedCompaction(files)
	if err != nil {
		return err
	}

	var live []fileInfo
	for _, fi := range files {
		switch {
		case fi.id < cut:
			os.Remove(segPath(db.dir, fi.id, fi.kind))
		case fi.kind == kindCmp && fi.id != cut:
			if files[0].id >= fi.id {
				return fmt.Errorf("%w: incomplete compaction output %s has no source segments", ErrCorrupt, segPath(db.dir, fi.id, fi.kind))
			}
			os.Remove(segPath(db.dir, fi.id, fi.kind))
		default:
			live = append(live, fi)
		}
	}

	for i, fi := range live {
		tail := i == len(live)-1 && fi.kind == kindSeg
		if err := db.replay(fi, tail); err != nil {
			return err
		}
	}

	for _, s := range db.segs {
		db.total += s.size
	}
	for _, e := range db.index {
		db.live += int64(e.size)
	}

	if len(live) > 0 && live[len(live)-1].kind == kindSeg {
		last := db.segs[live[len(live)-1].id]
		if last.size < db.opts.MaxSegmentSize {
			db.active = last
			return nil
		}
	}
	seg, err := db.createSegment()
	if err != nil {
		return err
	}
	db.segs[seg.id] = seg
	db.active = seg
	return nil
}

func (db *DB) findCommittedCompaction(files []fileInfo) (uint64, error) {
	for i := len(files) - 1; i >= 0; i-- {
		fi := files[i]
		if fi.kind != kindCmp {
			continue
		}
		ok, err := db.isCommitted(fi)
		if err != nil {
			return 0, err
		}
		if ok {
			return fi.id, nil
		}
	}
	return 0, nil
}

func (db *DB) isCommitted(fi fileInfo) (bool, error) {
	f, err := os.Open(segPath(db.dir, fi.id, fi.kind))
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	_, ok, err := readCommit(f, st.Size())
	return ok, err
}

func (db *DB) replay(fi fileInfo, tail bool) error {
	path := segPath(db.dir, fi.id, fi.kind)
	flag := os.O_RDONLY
	if tail {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return err
	}
	seg := &segment{id: fi.id, kind: fi.kind, f: f, path: path}
	db.segs[fi.id] = seg

	st, err := f.Stat()
	if err != nil {
		return err
	}
	size := st.Size()

	var puts uint64
	var commitCount uint64
	var commitOff int64 = -1
	end, err := scan(f, size, func(r record) error {
		if commitOff >= 0 {
			return fmt.Errorf("%w: %s has records after commit", ErrCorrupt, seg.name())
		}
		switch r.kind {
		case kindPut:
			puts++
			db.index[string(r.key)] = entry{id: fi.id, off: r.off, size: uint32(r.size)}
		case kindDelete:
			if fi.kind == kindCmp {
				return fmt.Errorf("%w: delete record in %s", ErrCorrupt, seg.name())
			}
			delete(db.index, string(r.key))
		case kindCommit:
			if fi.kind != kindCmp {
				return fmt.Errorf("%w: commit record in %s", ErrCorrupt, seg.name())
			}
			commitOff = r.off
			commitCount, _ = parseCommitBody(r.val)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if end != size {
		if !tail {
			return fmt.Errorf("%w: %s is damaged at offset %d", ErrCorrupt, seg.name(), end)
		}
		if err := f.Truncate(end); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if fi.kind == kindCmp && (commitOff < 0 || commitCount != puts || commitOff != end-commitRecordSize) {
		return fmt.Errorf("%w: %s has an inconsistent commit record", ErrCorrupt, seg.name())
	}
	seg.size = end
	return nil
}
