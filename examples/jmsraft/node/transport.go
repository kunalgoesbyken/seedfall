package main

import (
	"encoding/gob"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/jmsadair/raft"
)

const (
	appendEntries byte = iota
	requestVote
	installSnapshot
)

type tcpTransport struct {
	raft.Transport
	timeout  time.Duration
	mu       sync.Mutex
	listener net.Listener
	handlers map[byte]func(*gob.Decoder, *gob.Encoder) error
}

func newTCPTransport(address string, timeout time.Duration) (*tcpTransport, error) {
	stock, err := raft.NewTransport(address)
	if err != nil {
		return nil, err
	}
	return &tcpTransport{Transport: stock, timeout: timeout, handlers: map[byte]func(*gob.Decoder, *gob.Encoder) error{}}, nil
}

func (t *tcpTransport) Run() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listener != nil {
		return nil
	}
	ln, err := net.Listen("tcp", t.Address())
	if err != nil {
		return err
	}
	t.listener = ln
	go t.accept(ln)
	return nil
}

func (t *tcpTransport) Shutdown() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listener == nil {
		return nil
	}
	err := t.listener.Close()
	t.listener = nil
	return err
}

func (t *tcpTransport) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go t.serve(conn)
	}
}

func (t *tcpTransport) serve(conn net.Conn) {
	defer conn.Close()
	kind := make([]byte, 1)
	if _, err := conn.Read(kind); err != nil {
		return
	}
	t.mu.Lock()
	handle, ok := t.handlers[kind[0]]
	t.mu.Unlock()
	if ok {
		handle(gob.NewDecoder(conn), gob.NewEncoder(conn))
	}
}

func (t *tcpTransport) SendAppendEntries(address string, request raft.AppendEntriesRequest) (raft.AppendEntriesResponse, error) {
	var response raft.AppendEntriesResponse
	return response, t.call(address, appendEntries, request, &response)
}

func (t *tcpTransport) SendRequestVote(address string, request raft.RequestVoteRequest) (raft.RequestVoteResponse, error) {
	var response raft.RequestVoteResponse
	return response, t.call(address, requestVote, request, &response)
}

func (t *tcpTransport) SendInstallSnapshot(address string, request raft.InstallSnapshotRequest) (raft.InstallSnapshotResponse, error) {
	var response raft.InstallSnapshotResponse
	return response, t.call(address, installSnapshot, request, &response)
}

func (t *tcpTransport) RegisterAppendEntriesHandler(handler func(*raft.AppendEntriesRequest, *raft.AppendEntriesResponse) error) {
	register(t, appendEntries, handler)
}

func (t *tcpTransport) RegisterRequestVoteHandler(handler func(*raft.RequestVoteRequest, *raft.RequestVoteResponse) error) {
	register(t, requestVote, handler)
}

func (t *tcpTransport) RegsiterInstallSnapshotHandler(handler func(*raft.InstallSnapshotRequest, *raft.InstallSnapshotResponse) error) {
	register(t, installSnapshot, handler)
}

func register[Req, Resp any](t *tcpTransport, kind byte, handler func(*Req, *Resp) error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handlers[kind] = func(dec *gob.Decoder, enc *gob.Encoder) error {
		var request Req
		var response Resp
		if err := dec.Decode(&request); err != nil {
			return err
		}
		if err := handler(&request, &response); err != nil {
			return err
		}
		return enc.Encode(&response)
	}
}

func (t *tcpTransport) call(address string, kind byte, request, response any) error {
	if !t.running() {
		return errors.New("transport is closed")
	}
	conn, err := net.DialTimeout("tcp", address, t.timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(t.timeout))
	if _, err := conn.Write([]byte{kind}); err != nil {
		return err
	}
	if err := gob.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	return gob.NewDecoder(conn).Decode(response)
}

func (t *tcpTransport) running() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.listener != nil
}
