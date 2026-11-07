package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

type command struct {
	ID    uint64 `json:"id"`
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type store struct {
	id        uint64
	node      raft.Node
	storage   *raft.MemoryStorage
	wal       *wal
	transport *transport
	snapEvery uint64

	mu        sync.Mutex
	data      map[string]string
	waiters   map[uint64]chan string
	confState *pb.ConfState
	applied   uint64
	snapIndex uint64
}

func newStore(id uint64, w *wal, rec *recovered, peers map[uint64]string, snapEvery uint64) *store {
	s := &store{id: id, wal: w, snapEvery: snapEvery, storage: raft.NewMemoryStorage(), data: map[string]string{}, waiters: map[uint64]chan string{}, confState: &pb.ConfState{}}
	s.transport = newTransport(peers, id, func() raft.Node { return s.node })
	s.restore(rec)
	cfg := &raft.Config{ID: id, ElectionTick: 10, HeartbeatTick: 1, Storage: s.storage, Applied: s.applied, MaxSizePerMsg: 1 << 20, MaxInflightMsgs: 256, CheckQuorum: true, PreVote: true}
	if rec.hard == nil && len(rec.entries) == 0 && rec.snap == nil {
		s.node = raft.StartNode(cfg, peerList(peers))
		return s
	}
	s.node = raft.RestartNode(cfg)
	return s
}

func peerList(peers map[uint64]string) []raft.Peer {
	var out []raft.Peer
	for id := range uint64(len(peers)) {
		out = append(out, raft.Peer{ID: id + 1})
	}
	return out
}

func (s *store) restore(rec *recovered) {
	if rec.snap != nil {
		must(s.storage.ApplySnapshot(rec.snap))
		s.loadSnapshot(rec.snap)
	}
	if rec.hard != nil {
		must(s.storage.SetHardState(rec.hard))
	}
	must(s.storage.Append(rec.entries))
}

func (s *store) loadSnapshot(snap *pb.Snapshot) {
	data := map[string]string{}
	must(json.Unmarshal(snap.GetData(), &data))
	s.data = data
	s.confState = snap.GetMetadata().GetConfState()
	s.applied = snap.GetMetadata().GetIndex()
	s.snapIndex = s.applied
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func (s *store) run() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.node.Tick()
		case rd := <-s.node.Ready():
			s.process(rd)
		}
	}
}

func (s *store) process(rd raft.Ready) {
	snap := rd.Snapshot
	if raft.IsEmptySnap(snap) {
		snap = nil
	}
	if snap != nil {
		must(s.wal.saveSnapshot(snap))
	}
	hard := rd.HardState
	if raft.IsEmptyHardState(hard) {
		hard = nil
	}
	must(s.wal.save(hard, rd.Entries, snap))
	if snap != nil {
		must(s.storage.ApplySnapshot(snap))
		s.mu.Lock()
		s.loadSnapshot(snap)
		s.mu.Unlock()
	}
	if hard != nil {
		must(s.storage.SetHardState(hard))
	}
	must(s.storage.Append(rd.Entries))
	s.transport.send(rd.Messages)
	s.apply(rd.CommittedEntries)
	s.maybeSnapshot()
	s.node.Advance()
}

func (s *store) apply(entries []*pb.Entry) {
	for _, e := range entries {
		if e.GetIndex() <= s.applied {
			continue
		}
		switch e.GetType() {
		case pb.EntryType_EntryNormal:
			s.applyCommand(e.GetData())
		case pb.EntryType_EntryConfChange:
			cc := &pb.ConfChange{}
			must(proto.Unmarshal(e.GetData(), cc))
			s.confState = s.node.ApplyConfChange(cc)
		}
		s.applied = e.GetIndex()
	}
}

func (s *store) applyCommand(data []byte) {
	if len(data) == 0 {
		return
	}
	var c command
	must(json.Unmarshal(data, &c))
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := "OK"
	if c.Op == "put" {
		s.data[c.Key] = c.Value
	} else if v, ok := s.data[c.Key]; ok {
		reply = "VAL " + v
	} else {
		reply = "NONE"
	}
	if ch, ok := s.waiters[c.ID]; ok {
		ch <- reply
		delete(s.waiters, c.ID)
	}
}

func (s *store) maybeSnapshot() {
	if s.applied-s.snapIndex < s.snapEvery {
		return
	}
	s.mu.Lock()
	data, err := json.Marshal(s.data)
	s.mu.Unlock()
	must(err)
	snap, err := s.storage.CreateSnapshot(s.applied, s.confState, data)
	must(err)
	must(s.wal.saveSnapshot(snap))
	if s.applied > 10 {
		must(s.storage.Compact(s.applied - 10))
	}
	s.snapIndex = s.applied
}

func (s *store) do(c command) string {
	if s.node.Status().Lead != s.id {
		return "NOTLEADER"
	}
	c.ID = rand.Uint64()
	ch := make(chan string, 1)
	s.mu.Lock()
	s.waiters[c.ID] = ch
	s.mu.Unlock()
	defer s.forget(c.ID)
	data, err := json.Marshal(c)
	must(err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.node.Propose(ctx, data); err != nil {
		return "ERR " + err.Error()
	}
	select {
	case reply := <-ch:
		return reply
	case <-ctx.Done():
		return "ERR timeout"
	}
}

func (s *store) forget(id uint64) {
	s.mu.Lock()
	delete(s.waiters, id)
	s.mu.Unlock()
}
