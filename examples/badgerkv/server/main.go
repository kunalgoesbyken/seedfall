package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"strings"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/kunalgoesbyken/seedfall/sim"
)

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("db", "data", "database directory")
	syncWrites := flag.Bool("sync", true, "set Options.SyncWrites")
	flag.Parse()

	opts := badger.DefaultOptions(*dir).
		WithSyncWrites(*syncWrites).
		WithMemTableSize(1 << 20).
		WithValueLogFileSize(1 << 20).
		WithValueThreshold(1 << 10).
		WithNumCompactors(2).
		WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		log.Fatalf("open %s: %v", *dir, err)
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
		go serve(db, conn)
	}
}

func serve(db *badger.DB, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, handle(db, strings.Fields(sc.Text())))
	}
}

func handle(db *badger.DB, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "SET":
		err := db.Update(func(txn *badger.Txn) error {
			return txn.Set([]byte(f[1]), []byte(f[2]))
		})
		if err != nil {
			return "ERR " + err.Error()
		}
		return "OK"
	case len(f) == 2 && f[0] == "GET":
		return get(db, f[1])
	}
	return "ERR bad request"
}

func get(db *badger.DB, key string) string {
	var value []byte
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}
		value, err = item.ValueCopy(nil)
		return err
	})
	switch {
	case errors.Is(err, badger.ErrKeyNotFound):
		return "NONE"
	case err != nil:
		return "ERR " + err.Error()
	}
	return "VAL " + string(value)
}
