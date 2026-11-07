package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/kunalgoesbyken/seedfall/sim"
)

type fsm struct {
	mu     sync.Mutex
	values map[string]string
}

func (f *fsm) Apply(l *raft.Log) any {
	k, v, _ := strings.Cut(string(l.Data), "=")
	f.mu.Lock()
	f.values[k] = v
	f.mu.Unlock()
	return nil
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := json.Marshal(f.values)
	return snapshot(b), err
}

func (f *fsm) Restore(rc io.ReadCloser) error {
	sim.Reachable("a node restored from a snapshot")
	defer rc.Close()
	values := map[string]string{}
	if err := json.NewDecoder(rc).Decode(&values); err != nil {
		return err
	}
	f.mu.Lock()
	f.values = values
	f.mu.Unlock()
	return nil
}

func (f *fsm) get(k string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[k]
	return v, ok
}

type snapshot []byte

func (s snapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(s); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s snapshot) Release() {}

type streamLayer struct{ net.Listener }

func (s streamLayer) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	return sim.Dial("tcp", string(addr))
}

func main() {
	peers := flag.String("peers", "n1,n2,n3", "cluster members")
	raftPort := flag.String("raft-port", "7000", "raft port")
	apiPort := flag.String("api-port", "8000", "client API port")
	noSync := flag.Bool("no-fsync", false, "skip fsync in the log store (unsafe)")
	stockTCP := flag.Bool("tcp", false, "use raft's stock TCP transport and plain net.Listen (needs net_overlay)")
	flag.Parse()
	id := sim.Node()

	if err := os.MkdirAll("data/snapshots", 0o755); err != nil {
		log.Fatal(err)
	}
	store, err := openFileStore("data/raft.log", *noSync)
	if err != nil {
		log.Fatal(err)
	}
	snaps, err := raft.NewFileSnapshotStore("data/snapshots", 2, io.Discard)
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range []string{"data/snapshots", "data", "."} {
		if err := syncDir(d); err != nil {
			log.Fatal(err)
		}
	}
	transport, err := newTransport(*stockTCP, id, *raftPort)
	if err != nil {
		log.Fatal(err)
	}

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(id)
	cfg.HeartbeatTimeout = 200 * time.Millisecond
	cfg.ElectionTimeout = 200 * time.Millisecond
	cfg.LeaderLeaseTimeout = 100 * time.Millisecond
	cfg.CommitTimeout = 10 * time.Millisecond
	cfg.SnapshotThreshold = 40
	cfg.SnapshotInterval = 500 * time.Millisecond
	cfg.TrailingLogs = 20
	cfg.Logger = hclog.New(&hclog.LoggerOptions{Name: id, Level: hclog.Error, Output: os.Stderr})

	state := &fsm{values: map[string]string{}}
	r, err := raft.NewRaft(cfg, state, store, store, snaps, transport)
	if err != nil {
		log.Fatal(err)
	}
	if existing, _ := raft.HasExistingState(store, store, snaps); !existing {
		var servers []raft.Server
		for _, p := range strings.Split(*peers, ",") {
			servers = append(servers, raft.Server{ID: raft.ServerID(p), Address: raft.ServerAddress(p + ":" + *raftPort)})
		}
		r.BootstrapCluster(raft.Configuration{Servers: servers})
	}

	api, err := listen(*stockTCP, ":"+*apiPort)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := api.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go serve(conn, r, state)
	}
}

func serve(conn net.Conn, r *raft.Raft, state *fsm) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		fmt.Fprint(conn, handle(strings.Fields(line), r, state))
	}
}

func handle(cmd []string, r *raft.Raft, state *fsm) string {
	if r.State() != raft.Leader {
		return "NOTLEADER\n"
	}
	switch {
	case len(cmd) == 3 && cmd[0] == "PUT":
		if err := r.Apply([]byte(cmd[1]+"="+cmd[2]), time.Second).Error(); err != nil {
			return "ERR " + err.Error() + "\n"
		}
		return "OK\n"
	case len(cmd) == 2 && cmd[0] == "GET":
		if err := r.VerifyLeader().Error(); err != nil {
			return "NOTLEADER\n"
		}
		if v, ok := state.get(cmd[1]); ok {
			return "VAL " + v + "\n"
		}
		return "NONE\n"
	}
	return "ERR bad command\n"
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func listen(stockTCP bool, addr string) (net.Listener, error) {
	if stockTCP {
		return net.Listen("tcp", addr)
	}
	return sim.Listen("tcp", addr)
}

func newTransport(stockTCP bool, id, port string) (raft.Transport, error) {
	if !stockTCP {
		ln, err := sim.Listen("tcp", ":"+port)
		if err != nil {
			return nil, err
		}
		return raft.NewNetworkTransport(streamLayer{ln}, 3, time.Second, io.Discard), nil
	}
	advertise, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(id, port))
	if err != nil {
		return nil, err
	}
	return raft.NewTCPTransport(":"+port, advertise, 3, time.Second, io.Discard)
}
