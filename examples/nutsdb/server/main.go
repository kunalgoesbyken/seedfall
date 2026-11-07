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
	"github.com/nutsdb/nutsdb"
)

const bucket = "kv"

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data/db", "database directory")
	segment := flag.Int64("segment", 8*nutsdb.KB, "data file size in bytes")
	syncEnable := flag.Bool("sync", true, "fsync every commit (nutsdb SyncEnable)")
	fsyncMeta := flag.Bool("fsync-meta", false, "fsync bucket.Meta after creating the bucket, working around the library")
	flag.Parse()

	db, err := open(*dir, *segment, *syncEnable)
	if err != nil {
		log.Fatalf("open %s: %v", *dir, err)
	}
	if err := ensureBucket(db); err != nil {
		log.Fatalf("create bucket: %v", err)
	}
	if *fsyncMeta {
		if err := syncFile(filepath.Join(*dir, nutsdb.BucketStoreFileName)); err != nil {
			log.Fatalf("fsync bucket meta: %v", err)
		}
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

func open(dir string, segment int64, syncEnable bool) (*nutsdb.DB, error) {
	opts := nutsdb.DefaultOptions
	opts.Dir = dir
	opts.SegmentSize = segment
	opts.SyncEnable = syncEnable
	opts.RWMode = nutsdb.FileIO
	opts.MergeInterval = 0
	db, err := nutsdb.Open(opts)
	if err != nil {
		return nil, err
	}
	return db, syncDirs(dir)
}

func ensureBucket(db *nutsdb.DB) error {
	return db.Update(func(tx *nutsdb.Tx) error {
		if tx.ExistBucket(nutsdb.DataStructureBTree, bucket) {
			return nil
		}
		return tx.NewBucket(nutsdb.DataStructureBTree, bucket)
	})
}

func count(db *nutsdb.DB) int {
	var n int
	db.View(func(tx *nutsdb.Tx) error {
		keys, err := tx.GetKeys(bucket)
		n = len(keys)
		return err
	})
	return n
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

func serve(db *nutsdb.DB, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, handle(db, strings.Fields(sc.Text())))
	}
}

func handle(db *nutsdb.DB, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "SET":
		return set(db, f[1], f[2])
	case len(f) == 2 && f[0] == "GET":
		return get(db, f[1])
	}
	return "ERR bad request"
}

func set(db *nutsdb.DB, key, value string) string {
	err := db.Update(func(tx *nutsdb.Tx) error {
		return tx.Put(bucket, []byte(key), []byte(value), nutsdb.Persistent)
	})
	if err != nil {
		return "ERR " + err.Error()
	}
	return "OK"
}

func get(db *nutsdb.DB, key string) string {
	var value []byte
	err := db.View(func(tx *nutsdb.Tx) error {
		v, err := tx.Get(bucket, []byte(key))
		value = v
		return err
	})
	switch {
	case errors.Is(err, nutsdb.ErrKeyNotFound):
		return "NONE"
	case err != nil:
		return "ERR " + err.Error()
	}
	return "VAL " + string(value)
}
