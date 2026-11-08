package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type store interface {
	Put(key string, value []byte) error
	Get(key string) ([]byte, bool, error)
	Delete(key string) error
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data/db", "data directory")
	impl := flag.String("impl", "", "implementation to serve")
	flag.Parse()

	open, ok := impls[*impl]
	if !ok {
		log.Fatalf("unknown -impl %q, have %s", *impl, strings.Join(slices.Sorted(maps.Keys(impls)), ", "))
	}
	if err := os.MkdirAll(filepath.Dir(*dir), 0o755); err != nil {
		log.Fatal(err)
	}
	db, err := open(*dir)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	fmt.Printf("%s opened %s\n", *impl, *dir)
	ln, err := sim.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go serve(db, conn)
	}
}

func serve(db store, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, handle(db, strings.Fields(sc.Text())))
	}
}

func handle(db store, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "PUT":
		return reply(db.Put(f[1], []byte(f[2])))
	case len(f) == 2 && f[0] == "DEL":
		return reply(db.Delete(f[1]))
	case len(f) == 2 && f[0] == "GET":
		v, found, err := db.Get(f[1])
		switch {
		case err != nil:
			return "ERR " + err.Error()
		case !found:
			return "NONE"
		}
		return "VAL " + string(v)
	}
	return "ERR bad request"
}

func reply(err error) string {
	if err != nil {
		return "ERR " + err.Error()
	}
	return "OK"
}
