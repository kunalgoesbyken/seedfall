package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const currentLink = "current"

type store struct {
	mu      sync.Mutex
	dir     string
	gen     int
	values  map[string]string
	syncDir bool
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data", "data directory")
	syncDirFirst := flag.Bool("sync-dir", false, "fsync the directory after creating a generation, before pointing the symlink at it")
	flag.Parse()
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := fsyncDir(filepath.Dir(*dir)); err != nil {
		log.Fatal(err)
	}
	if err := fsyncDir(*dir); err != nil {
		log.Fatal(err)
	}
	s := &store{dir: *dir, values: map[string]string{}, syncDir: *syncDirFirst}
	s.recover()
	ln, err := sim.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("serving %d keys from generation %d on %s\n", len(s.values), s.gen, *addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go s.serve(conn)
	}
}

func (s *store) recover() {
	link := filepath.Join(s.dir, currentLink)
	target, err := os.Readlink(link)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	s.gen, err = strconv.Atoi(strings.TrimPrefix(target, "gen."))
	if err != nil {
		log.Fatal(err)
	}
	data, err := os.ReadFile(link)
	sim.Always(err == nil, "the current symlink points at a generation that exists", fmt.Sprintf("%s -> %s: %v", currentLink, target, err))
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			s.values[k] = v
		}
	}
	info, err := os.Lstat(link)
	sim.Always(err == nil && info.Mode()&os.ModeSymlink != 0, "current is still a symlink", fmt.Sprint(err))
}

func (s *store) put(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.gen + 1
	name := fmt.Sprintf("gen.%d", next)
	values := map[string]string{k: v}
	for key, val := range s.values {
		if key != k {
			values[key] = val
		}
	}
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, "%s=%s\n", key, values[key])
	}
	if err := writeSynced(filepath.Join(s.dir, name), b.String()); err != nil {
		return err
	}
	if s.syncDir {
		if err := fsyncDir(s.dir); err != nil {
			return err
		}
	}
	tmp := filepath.Join(s.dir, "current.tmp")
	os.Remove(tmp)
	if err := os.Symlink(name, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, currentLink)); err != nil {
		return err
	}
	if err := fsyncDir(s.dir); err != nil {
		return err
	}
	if next > 2 {
		os.Remove(filepath.Join(s.dir, fmt.Sprintf("gen.%d", next-2)))
	}
	s.gen, s.values = next, values
	return nil
}

func writeSynced(path, content string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return err
	}
	return f.Sync()
}

func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *store) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "PUT":
			if err := s.put(fields[1], fields[2]); err != nil {
				fmt.Fprintf(conn, "ERR %v\n", err)
				continue
			}
			fmt.Fprint(conn, "OK\n")
		case len(fields) == 2 && fields[0] == "GET":
			s.mu.Lock()
			v, ok := s.values[fields[1]]
			s.mu.Unlock()
			if ok {
				fmt.Fprintf(conn, "VAL %s\n", v)
			} else {
				fmt.Fprint(conn, "MISS\n")
			}
		default:
			fmt.Fprint(conn, "ERR bad request\n")
		}
	}
}
