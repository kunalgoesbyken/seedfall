package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
)

func main() {
	id := flag.Uint64("id", 0, "raft node id")
	cluster := flag.String("cluster", "1=n1:9000,2=n2:9000,3=n3:9000", "raft peers as id=host:port")
	api := flag.String("api", ":8000", "client API address")
	noSync := flag.Bool("no-fsync", false, "skip fsync of the WAL (unsafe)")
	snapEvery := flag.Uint64("snapshot-every", 50, "applied entries between snapshots")
	flag.Parse()

	peers, err := parseCluster(*cluster)
	if err != nil {
		log.Fatal(err)
	}
	w, rec, err := openWAL("data", *noSync)
	if err != nil {
		log.Fatalf("open wal: %v", err)
	}
	fmt.Printf("recovered %d entries, snapshot at %d\n", len(rec.entries), rec.snap.GetMetadata().GetIndex())
	kv := newStore(*id, w, rec, peers, *snapEvery)
	go func() { log.Fatal(kv.transport.serve(peers[*id][strings.LastIndex(peers[*id], ":"):])) }()
	go kv.run()
	log.Fatal(serveAPI(*api, kv))
}

func parseCluster(s string) (map[uint64]string, error) {
	peers := map[uint64]string{}
	for _, part := range strings.Split(s, ",") {
		id, addr, ok := strings.Cut(part, "=")
		n, err := strconv.ParseUint(id, 10, 64)
		if !ok || err != nil {
			return nil, fmt.Errorf("bad peer %q", part)
		}
		peers[n] = addr
	}
	return peers, nil
}

func serveAPI(addr string, kv *store) error {
	ln, err := sim.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handle(conn, kv)
	}
}

func handle(conn net.Conn, kv *store) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, respond(kv, strings.Fields(sc.Text())))
	}
}

func respond(kv *store, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "PUT":
		return kv.do(command{Op: "put", Key: f[1], Value: f[2]})
	case len(f) == 2 && f[0] == "GET":
		return kv.do(command{Op: "get", Key: f[1]})
	}
	return "ERR bad request"
}
