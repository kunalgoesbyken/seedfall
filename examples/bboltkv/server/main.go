package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
	bolt "go.etcd.io/bbolt"
)

var bucket = []byte("kv")

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	path := flag.String("db", "data/kv.db", "database file")
	noSync := flag.Bool("nosync", false, "set DB.NoSync")
	syncDir := flag.Bool("sync-dir", true, "fsync the database directory after Open")
	flag.Parse()

	db, err := open(*path, *noSync, *syncDir)
	if err != nil {
		log.Fatalf("open %s: %v", *path, err)
	}
	fmt.Printf("recovered %d keys\n", count(db))

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

func open(path string, noSync, syncDir bool) (*bolt.DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := syncDirs("."); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, NoSync: noSync})
	if err != nil {
		return nil, err
	}
	if syncDir {
		if err := syncDirs(dir); err != nil {
			return nil, err
		}
	}
	return db, db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	})
}

func count(db *bolt.DB) int {
	var n int
	db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucket); b != nil {
			n = b.Stats().KeyN
		}
		return nil
	})
	return n
}

func syncDirs(dirs ...string) error {
	for _, d := range dirs {
		f, err := os.Open(d)
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func serve(db *bolt.DB, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, handle(db, strings.Fields(sc.Text())))
	}
}

func handle(db *bolt.DB, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "SET":
		return set(db, f[1], f[2])
	case len(f) == 2 && f[0] == "GET":
		return get(db, f[1])
	}
	return "ERR bad request"
}

func set(db *bolt.DB, key, value string) string {
	err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(key), []byte(value))
	})
	if err != nil {
		return "ERR " + err.Error()
	}
	return "OK"
}

func get(db *bolt.DB, key string) string {
	var value []byte
	err := db.View(func(tx *bolt.Tx) error {
		value = tx.Bucket(bucket).Get([]byte(key))
		if value != nil {
			value = append([]byte{}, value...)
		}
		return nil
	})
	switch {
	case err != nil:
		return "ERR " + err.Error()
	case value == nil:
		return "NONE"
	}
	return "VAL " + string(value)
}
