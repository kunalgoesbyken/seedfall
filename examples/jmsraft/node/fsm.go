package main

import (
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/jmsadair/raft"
)

type kv struct {
	mu            sync.RWMutex
	values        map[string]string
	snapshotEvery int
}

func newKV(snapshotEvery int) *kv {
	return &kv{values: map[string]string{}, snapshotEvery: snapshotEvery}
}

func (s *kv) Apply(op *raft.Operation) interface{} {
	request := string(op.Bytes)
	if op.OperationType == raft.Replicated {
		return s.put(request)
	}
	return s.query(request)
}

func (s *kv) put(request string) string {
	_, rest, _ := strings.Cut(request, " ")
	key, value, _ := strings.Cut(rest, " ")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return "OK"
}

func (s *kv) query(request string) string {
	_, key, _ := strings.Cut(request, " ")
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[key]
	if !ok {
		return "NONE"
	}
	return "VAL " + value
}

func (s *kv) Snapshot(w io.Writer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.NewEncoder(w).Encode(s.values)
}

func (s *kv) Restore(r io.Reader) error {
	values := map[string]string{}
	if err := json.NewDecoder(r).Decode(&values); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = values
	return nil
}

func (s *kv) NeedSnapshot(logSize int) bool {
	return s.snapshotEvery > 0 && logSize >= s.snapshotEvery
}
