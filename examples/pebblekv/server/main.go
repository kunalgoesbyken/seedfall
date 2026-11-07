package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/cockroachdb/pebble/v2"
	"github.com/kunalgoesbyken/seedfall/sim"
)

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("db", "data", "database directory")
	noSync := flag.Bool("nosync", false, "write with pebble.NoSync")
	flag.Parse()

	db, err := pebble.Open(*dir, &pebble.Options{})
	if err != nil {
		log.Fatalf("open %s: %v", *dir, err)
	}
	opts := pebble.Sync
	if *noSync {
		opts = pebble.NoSync
	}

	ln, err := sim.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go serve(db, opts, conn)
	}
}

func serve(db *pebble.DB, opts *pebble.WriteOptions, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, handle(db, opts, strings.Fields(sc.Text())))
	}
}

func handle(db *pebble.DB, opts *pebble.WriteOptions, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "SET":
		if err := db.Set([]byte(f[1]), []byte(f[2]), opts); err != nil {
			return "ERR " + err.Error()
		}
		return "OK"
	case len(f) == 2 && f[0] == "GET":
		return get(db, f[1])
	}
	return "ERR bad request"
}

func get(db *pebble.DB, key string) string {
	value, closer, err := db.Get([]byte(key))
	switch {
	case errors.Is(err, pebble.ErrNotFound):
		return "NONE"
	case err != nil:
		return "ERR " + err.Error()
	}
	defer closer.Close()
	return "VAL " + string(value)
}
