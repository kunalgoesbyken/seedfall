package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

type transport struct {
	node  func() raft.Node
	peers map[uint64]chan *pb.Message
}

func newTransport(addrs map[uint64]string, self uint64, node func() raft.Node) *transport {
	t := &transport{node: node, peers: map[uint64]chan *pb.Message{}}
	for id, addr := range addrs {
		if id == self {
			continue
		}
		ch := make(chan *pb.Message, 256)
		t.peers[id] = ch
		go t.sendLoop(id, addr, ch)
	}
	return t
}

func (t *transport) send(msgs []*pb.Message) {
	for _, m := range msgs {
		select {
		case t.peers[m.GetTo()] <- m:
		default:
			t.node().ReportUnreachable(m.GetTo())
		}
	}
}

func (t *transport) sendLoop(id uint64, addr string, ch chan *pb.Message) {
	var w *bufio.Writer
	var conn net.Conn
	for m := range ch {
		if conn == nil {
			conn, w = dialPeer(addr)
		}
		if conn == nil || writeFrame(w, m) != nil {
			t.fail(id, m)
			if conn != nil {
				conn.Close()
			}
			conn = nil
		}
	}
}

func (t *transport) fail(id uint64, m *pb.Message) {
	t.node().ReportUnreachable(id)
	if m.GetType() == pb.MessageType_MsgSnap {
		t.node().ReportSnapshot(id, raft.SnapshotFailure)
	}
}

func dialPeer(addr string) (net.Conn, *bufio.Writer) {
	conn, err := sim.Dial("tcp", addr)
	if err != nil {
		return nil, nil
	}
	return conn, bufio.NewWriter(conn)
}

func writeFrame(w *bufio.Writer, m *pb.Message) error {
	b := mustMarshal(m)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	return w.Flush()
}

func (t *transport) serve(addr string) error {
	ln, err := sim.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go t.receive(conn)
	}
}

func (t *transport) receive(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		m, err := readFrame(r)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		t.node().Step(ctx, m)
		cancel()
	}
}

func readFrame(r *bufio.Reader) (*pb.Message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > 64<<20 {
		return nil, fmt.Errorf("frame of %d bytes is too large", size)
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	m := &pb.Message{}
	return m, proto.Unmarshal(b, m)
}
