package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"sync"

	"github.com/hashicorp/raft"
)

const (
	recordLog byte = iota + 1
	recordDelete
	recordStable
)

type record struct {
	Kind  byte
	Log   *raft.Log `json:",omitempty"`
	Min   uint64    `json:",omitempty"`
	Max   uint64    `json:",omitempty"`
	Key   []byte    `json:",omitempty"`
	Value []byte    `json:",omitempty"`
}

type fileStore struct {
	mu     sync.Mutex
	file   *os.File
	logs   map[uint64]*raft.Log
	first  uint64
	last   uint64
	stable map[string][]byte
	noSync bool
}

var errNotFound = errors.New("not found")

func openFileStore(path string, noSync bool) (*fileStore, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	s := &fileStore{logs: map[uint64]*raft.Log{}, stable: map[string][]byte{}, noSync: noSync}
	valid := s.replay(data)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(int64(valid)); err != nil {
		return nil, err
	}
	if _, err := f.Seek(int64(valid), io.SeekStart); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	s.file = f
	return s, nil
}

func (s *fileStore) replay(data []byte) int {
	off := 0
	for off+8 <= len(data) {
		size := int(binary.LittleEndian.Uint32(data[off:]))
		sum := binary.LittleEndian.Uint32(data[off+4:])
		end := off + 8 + size
		if end > len(data) || crc32.ChecksumIEEE(data[off+8:end]) != sum {
			break
		}
		var r record
		if json.Unmarshal(data[off+8:end], &r) != nil {
			break
		}
		s.apply(r)
		off = end
	}
	return off
}

func (s *fileStore) apply(r record) {
	switch r.Kind {
	case recordLog:
		s.logs[r.Log.Index] = r.Log
		if s.first == 0 || r.Log.Index < s.first {
			s.first = r.Log.Index
		}
		s.last = max(s.last, r.Log.Index)
	case recordDelete:
		for i := r.Min; i <= r.Max; i++ {
			delete(s.logs, i)
		}
		s.recomputeBounds()
	case recordStable:
		s.stable[string(r.Key)] = r.Value
	}
}

func (s *fileStore) recomputeBounds() {
	s.first, s.last = 0, 0
	for i := range s.logs {
		if s.first == 0 || i < s.first {
			s.first = i
		}
		s.last = max(s.last, i)
	}
}

func (s *fileStore) persist(records ...record) error {
	var buf bytes.Buffer
	for _, r := range records {
		payload, err := json.Marshal(r)
		if err != nil {
			return err
		}
		var header [8]byte
		binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
		binary.LittleEndian.PutUint32(header[4:], crc32.ChecksumIEEE(payload))
		buf.Write(header[:])
		buf.Write(payload)
	}
	if _, err := s.file.Write(buf.Bytes()); err != nil {
		return err
	}
	if !s.noSync {
		if err := s.file.Sync(); err != nil {
			return err
		}
	}
	for _, r := range records {
		s.apply(r)
	}
	return nil
}

func (s *fileStore) FirstIndex() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.first, nil
}

func (s *fileStore) LastIndex() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, nil
}

func (s *fileStore) GetLog(index uint64, out *raft.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.logs[index]
	if !ok {
		return raft.ErrLogNotFound
	}
	*out = *l
	return nil
}

func (s *fileStore) StoreLog(l *raft.Log) error { return s.StoreLogs([]*raft.Log{l}) }

func (s *fileStore) StoreLogs(logs []*raft.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]record, len(logs))
	for i, l := range logs {
		records[i] = record{Kind: recordLog, Log: l}
	}
	return s.persist(records...)
}

func (s *fileStore) DeleteRange(min, max uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persist(record{Kind: recordDelete, Min: min, Max: max})
}

func (s *fileStore) Set(key, val []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persist(record{Kind: recordStable, Key: key, Value: val})
}

func (s *fileStore) Get(key []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.stable[string(key)]
	if !ok {
		return nil, errNotFound
	}
	return v, nil
}

func (s *fileStore) SetUint64(key []byte, val uint64) error {
	return s.Set(key, binary.BigEndian.AppendUint64(nil, val))
}

func (s *fileStore) GetUint64(key []byte) (uint64, error) {
	v, err := s.Get(key)
	if err != nil {
		return 0, nil
	}
	return binary.BigEndian.Uint64(v), nil
}
