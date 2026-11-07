package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"syscall"
	"time"

	"github.com/jmsadair/raft"
	"github.com/jmsadair/raft/logging"
	"github.com/kunalgoesbyken/seedfall/sim"
)

var readModes = map[string]raft.OperationType{
	"linearizable": raft.LinearizableReadOnly,
	"lease":        raft.LeaseBasedReadOnly,
}

func main() {
	id := flag.String("id", sim.Node(), "node ID")
	peers := flag.String("peers", "n1=n1:9000,n2=n2:9000,n3=n3:9000", "cluster members as id=address")
	addr := flag.String("api", ":8000", "client API address")
	reads := flag.String("reads", "linearizable", "read path through raft: linearizable or lease")
	localReads := flag.Bool("local-reads", false, "serve reads from the local state machine, bypassing raft")
	snapshotEvery := flag.Int("snapshot-every", 32, "log entries between snapshots, 0 to disable")
	timeout := flag.Duration("timeout", time.Second, "operation timeout")
	transport := flag.String("transport", "tcp", "raft transport: tcp, or grpc for the library's stock transport")
	rpcTimeout := flag.Duration("rpc-timeout", 2*time.Second, "deadline of one raft RPC over the tcp transport")
	flag.Parse()
	kind, ok := readModes[*reads]
	if !ok {
		log.Fatalf("unknown read mode %q", *reads)
	}

	members := parseMembers(*peers)
	fsm := newKV(*snapshotEvery)
	node, err := start(*id, members, fsm, *transport, *rpcTimeout)
	if errors.Is(err, syscall.EIO) {
		log.Fatal(err)
	}
	if err != nil {
		sim.Fail("node restarts from its own disk: " + err.Error())
	}
	a := &api{raft: node, fsm: fsm, reads: kind, local: *localReads, timeout: *timeout}
	log.Fatal(a.serve(*addr))
}

func start(id string, members map[string]string, fsm *kv, transport string, rpcTimeout time.Duration) (*raft.Raft, error) {
	opts, err := options(members[id], transport, rpcTimeout)
	if err != nil {
		return nil, err
	}
	node, err := raft.NewRaft(id, members[id], fsm, "data", opts...)
	if err != nil {
		return nil, err
	}
	if len(node.Configuration().Members) == 0 {
		if err := node.Bootstrap(members); err != nil {
			return nil, err
		}
	}
	return node, node.Start()
}

func options(address, transport string, rpcTimeout time.Duration) ([]raft.Option, error) {
	opts := []raft.Option{raft.WithLogLevel(logging.Error)}
	switch transport {
	case "grpc":
		return opts, nil
	case "tcp":
		t, err := newTCPTransport(address, rpcTimeout)
		return append(opts, raft.WithTransport(t)), err
	}
	return nil, fmt.Errorf("unknown transport %q", transport)
}

func parseMembers(list string) map[string]string {
	members := map[string]string{}
	for _, member := range strings.Split(list, ",") {
		id, address, _ := strings.Cut(member, "=")
		members[id] = address
	}
	return members
}
