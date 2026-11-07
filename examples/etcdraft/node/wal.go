package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

const (
	recEntry byte = iota + 1
	recHardState
	recSnapshot
)

type wal struct {
	f      *os.File
	dir    string
	noSync bool
}

type recovered struct {
	snap    *pb.Snapshot
	hard    *pb.HardState
	entries []*pb.Entry
}

func openWAL(dir string, noSync bool) (*wal, *recovered, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	snap, err := loadSnapshot(dir)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "wal"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, err
	}
	rec, end, err := replay(f, snap)
	if err != nil {
		return nil, nil, err
	}
	if err := truncateTail(f, end); err != nil {
		return nil, nil, err
	}
	w := &wal{f: f, dir: dir, noSync: noSync}
	if err := syncDir(filepath.Dir(dir)); err != nil {
		return nil, nil, err
	}
	return w, rec, syncDir(dir)
}

func truncateTail(f *os.File, end int64) error {
	if err := f.Truncate(end); err != nil {
		return err
	}
	_, err := f.Seek(end, io.SeekStart)
	return err
}

func replay(f *os.File, snap *pb.Snapshot) (*recovered, int64, error) {
	rec := &recovered{snap: snap}
	r := bufio.NewReader(f)
	var end int64
	for {
		kind, payload, n, err := readRecord(r)
		if err != nil {
			return rec, end, nil
		}
		if err := rec.apply(kind, payload); err != nil {
			return nil, 0, err
		}
		end += n
	}
}

func readRecord(r *bufio.Reader) (byte, []byte, int64, error) {
	var hdr [9]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, 0, err
	}
	size := binary.BigEndian.Uint32(hdr[1:5])
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, 0, err
	}
	if crc32.ChecksumIEEE(append([]byte{hdr[0]}, body...)) != binary.BigEndian.Uint32(hdr[5:9]) {
		return 0, nil, 0, errors.New("checksum mismatch")
	}
	return hdr[0], body, int64(len(hdr)) + int64(size), nil
}

func (rec *recovered) apply(kind byte, payload []byte) error {
	switch kind {
	case recEntry:
		e := &pb.Entry{}
		if err := proto.Unmarshal(payload, e); err != nil {
			return err
		}
		rec.appendEntry(e)
	case recHardState:
		rec.hard = &pb.HardState{}
		return proto.Unmarshal(payload, rec.hard)
	case recSnapshot:
		rec.entries = nil
	}
	return nil
}

func (rec *recovered) appendEntry(e *pb.Entry) {
	if e.GetIndex() <= rec.snap.GetMetadata().GetIndex() {
		return
	}
	for len(rec.entries) > 0 && rec.entries[len(rec.entries)-1].GetIndex() >= e.GetIndex() {
		rec.entries = rec.entries[:len(rec.entries)-1]
	}
	rec.entries = append(rec.entries, e)
}

func (w *wal) save(hard *pb.HardState, entries []*pb.Entry, snap *pb.Snapshot) error {
	var buf []byte
	if snap != nil {
		buf = appendRecord(buf, recSnapshot, nil)
	}
	for _, e := range entries {
		buf = appendRecord(buf, recEntry, mustMarshal(e))
	}
	if hard != nil {
		buf = appendRecord(buf, recHardState, mustMarshal(hard))
	}
	if len(buf) == 0 {
		return nil
	}
	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	return w.sync(w.f)
}

func (w *wal) sync(f *os.File) error {
	if w.noSync {
		return nil
	}
	return f.Sync()
}

func appendRecord(buf []byte, kind byte, payload []byte) []byte {
	var hdr [9]byte
	hdr[0] = kind
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[5:9], crc32.ChecksumIEEE(append([]byte{kind}, payload...)))
	return append(append(buf, hdr[:]...), payload...)
}

func mustMarshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func (w *wal) saveSnapshot(snap *pb.Snapshot) error {
	tmp := filepath.Join(w.dir, "snap.tmp")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(mustMarshal(snap)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(w.dir, "snap")); err != nil {
		return err
	}
	return syncDir(w.dir)
}

func loadSnapshot(dir string) (*pb.Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, "snap"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snap := &pb.Snapshot{}
	return snap, proto.Unmarshal(b, snap)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
