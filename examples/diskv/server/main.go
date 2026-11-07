package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
	"github.com/peterbourgon/diskv/v3"
)

type store struct {
	db   *diskv.Diskv
	sync bool
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data/db", "base directory")
	tempDir := flag.String("temp", "data/tmp", "temp directory for atomic writes, empty writes in place")
	syncWrites := flag.Bool("sync", true, "fsync every write (WriteStream with sync)")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := syncDirs(*dir); err != nil {
		log.Fatalf("sync dirs: %v", err)
	}
	s := store{db: diskv.New(diskv.Options{BasePath: *dir, TempDir: *tempDir}), sync: *syncWrites}
	fmt.Printf("recovered %d keys\n", s.count())

	ln, err := sim.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go s.serve(conn)
	}
}

func (s store) count() int {
	var n int
	for range s.db.Keys(nil) {
		n++
	}
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

func (s store) serve(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		fmt.Fprintln(conn, s.handle(strings.Fields(sc.Text())))
	}
}

func (s store) handle(f []string) string {
	switch {
	case len(f) == 3 && f[0] == "SET":
		return s.set(f[1], f[2])
	case len(f) == 2 && f[0] == "GET":
		return s.get(f[1])
	}
	return "ERR bad request"
}

func (s store) set(key, value string) string {
	if err := s.db.WriteStream(key, bytes.NewReader([]byte(value)), s.sync); err != nil {
		return "ERR " + err.Error()
	}
	return "OK"
}

func (s store) get(key string) string {
	value, err := s.db.Read(key)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "NONE"
	case err != nil:
		return "ERR " + err.Error()
	}
	return "VAL " + string(value)
}
