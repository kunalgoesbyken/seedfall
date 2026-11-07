package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
	"go.mills.io/bitcask/v2"
)

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data/db", "database directory")
	datafile := flag.Uint64("datafile", 4096, "maximum datafile size in bytes")
	syncWrites := flag.Bool("sync", true, "fsync every Put (bitcask WithSyncWrites)")
	autoRecovery := flag.Bool("autorecovery", true, "repair a corrupted last datafile on open (bitcask WithAutoRecovery)")
	fsyncConfig := flag.Bool("fsync-config", false, "fsync config.json after Open, working around the library")
	flag.Parse()

	db, err := bitcask.Open(*dir,
		bitcask.WithSyncWrites(*syncWrites),
		bitcask.WithAutoRecovery(*autoRecovery),
		bitcask.WithMaxDatafileSize(*datafile),
	)
	if err != nil {
		log.Fatalf("open %s: %v", *dir, err)
	}
	if *fsyncConfig {
		if err := syncFile(filepath.Join(*dir, "config.json")); err != nil {
			log.Fatalf("fsync config: %v", err)
		}
	}
	if err := syncDirs(*dir); err != nil {
		log.Fatalf("sync dirs: %v", err)
	}
	fmt.Printf("recovered %d keys\n", db.Len())

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

func syncDirs(dir string) error {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if err := syncFile(d); err != nil {
			return err
		}
		if d == filepath.Dir(d) {
			return nil
		}
	}
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func serve(db *bitcask.Bitcask, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, handle(db, strings.Fields(sc.Text())))
	}
}

func handle(db *bitcask.Bitcask, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "SET":
		return set(db, f[1], f[2])
	case len(f) == 2 && f[0] == "GET":
		return get(db, f[1])
	}
	return "ERR bad request"
}

func set(db *bitcask.Bitcask, key, value string) string {
	if err := db.Put([]byte(key), []byte(value)); err != nil {
		return "ERR " + err.Error()
	}
	return "OK"
}

func get(db *bitcask.Bitcask, key string) string {
	value, err := db.Get([]byte(key))
	switch {
	case errors.Is(err, bitcask.ErrKeyNotFound):
		return "NONE"
	case err != nil:
		return "ERR " + err.Error()
	}
	return "VAL " + string(value)
}
