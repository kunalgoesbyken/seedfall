package main

import (
	"flag"
	"io"
	"log"
	"net"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
	"github.com/nats-io/nats-server/v2/server"
)

func main() {
	syncAlways := flag.Bool("sync-always", false, "fsync every JetStream write before acknowledging")
	flag.Parse()
	ns := start(*syncAlways)
	ln, err := sim.Listen("tcp", ":4222")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving, sync_always=%v", *syncAlways)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go proxy(ns, c)
	}
}

func start(syncAlways bool) *server.Server {
	ns, err := server.NewServer(&server.Options{
		ServerName:         sim.Node(),
		JetStream:          true,
		StoreDir:           "data",
		JetStreamMaxMemory: 64 << 20,
		JetStreamMaxStore:  1 << 30,
		DontListen:         true,
		NoSigs:             true,
		SyncAlways:         syncAlways,
	})
	if err != nil {
		log.Fatal(err)
	}
	ns.Start()
	if !ns.ReadyForConnections(30 * time.Second) {
		log.Fatal("nats-server did not become ready")
	}
	return ns
}

func proxy(ns *server.Server, c net.Conn) {
	defer c.Close()
	s, err := ns.InProcessConn()
	if err != nil {
		return
	}
	defer s.Close()
	go func() {
		io.Copy(s, c)
		s.Close()
	}()
	io.Copy(c, s)
}
