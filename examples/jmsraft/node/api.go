package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jmsadair/raft"
)

type api struct {
	raft    *raft.Raft
	fsm     *kv
	reads   raft.OperationType
	local   bool
	timeout time.Duration
}

func (a *api) serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go a.reply(conn)
	}
}

func (a *api) reply(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	fmt.Fprintf(conn, "%s\n", a.handle(strings.TrimSpace(line)))
}

func (a *api) handle(request string) string {
	verb, _, _ := strings.Cut(request, " ")
	switch {
	case verb == "PUT":
		return a.submit(request, raft.Replicated)
	case verb == "GET" && a.local:
		return a.fsm.query(request)
	case verb == "GET":
		return a.submit(request, a.reads)
	}
	return "ERR unknown request"
}

func (a *api) submit(request string, kind raft.OperationType) string {
	if a.raft.Status().State != raft.Leader {
		return "NOTLEADER"
	}
	result := a.raft.SubmitOperation([]byte(request), kind, a.timeout).Await()
	if err := result.Error(); err != nil {
		return "ERR " + err.Error()
	}
	return result.Success().ApplicationResponse.(string)
}
