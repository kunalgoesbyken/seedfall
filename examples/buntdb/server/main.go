package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
	"github.com/tidwall/buntdb"
)

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	path := flag.String("db", "data/db", "database file")
	syncPolicy := flag.String("sync", "always", "always or everysecond")
	shrinkAt := flag.Int("shrink-at", 0, "auto-shrink minimum size in bytes, 0 keeps the default")
	flag.Parse()

	db, err := open(*path, *syncPolicy, *shrinkAt)
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

func open(path, syncPolicy string, shrinkAt int) (*buntdb.DB, error) {
	policies := map[string]buntdb.SyncPolicy{"always": buntdb.Always, "everysecond": buntdb.EverySecond}
	policy, ok := policies[syncPolicy]
	if !ok {
		return nil, fmt.Errorf("unknown sync policy %q", syncPolicy)
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, err
	}
	db, err := buntdb.Open(path)
	if err != nil {
		return nil, err
	}
	if err := syncDirs(dirOf(path)); err != nil {
		return nil, err
	}
	var cfg buntdb.Config
	if err := db.ReadConfig(&cfg); err != nil {
		return nil, err
	}
	cfg.SyncPolicy = policy
	if shrinkAt > 0 {
		cfg.AutoShrinkMinSize = shrinkAt
		cfg.AutoShrinkPercentage = 50
	}
	return db, db.SetConfig(cfg)
}

func count(db *buntdb.DB) int {
	var n int
	db.View(func(tx *buntdb.Tx) error {
		n, _ = tx.Len()
		return nil
	})
	return n
}

func syncDirs(dir string) error {
	for _, d := range []string{dir, "."} {
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

func dirOf(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return "."
}

func serve(db *buntdb.DB, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, handle(db, strings.Fields(sc.Text())))
	}
}

func handle(db *buntdb.DB, f []string) string {
	switch {
	case len(f) == 3 && f[0] == "SET":
		return set(db, f[1], f[2])
	case len(f) == 2 && f[0] == "GET":
		return get(db, f[1])
	}
	return "ERR bad request"
}

func set(db *buntdb.DB, key, value string) string {
	err := db.Update(func(tx *buntdb.Tx) error {
		_, _, err := tx.Set(key, value, nil)
		return err
	})
	if err != nil {
		return "ERR " + err.Error()
	}
	return "OK"
}

func get(db *buntdb.DB, key string) string {
	var value string
	err := db.View(func(tx *buntdb.Tx) error {
		v, err := tx.Get(key)
		value = v
		return err
	})
	switch {
	case errors.Is(err, buntdb.ErrNotFound):
		return "NONE"
	case err != nil:
		return "ERR " + err.Error()
	}
	return "VAL " + value
}
