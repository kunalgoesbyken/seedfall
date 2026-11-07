package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type store struct {
	mu     sync.Mutex
	file   *os.File
	values map[string]string
	always bool
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data", "data directory")
	syncMode := flag.String("sync", "batch", "batch: fsync every 200ms, always: fsync before every ack")
	repair := flag.Bool("repair", false, "truncate a torn log tail during recovery")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	s, err := open(*dir+"/kv.log", *repair)
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range []string{*dir, filepath.Dir(*dir)} {
		if err := syncDir(d); err != nil {
			log.Fatal(err)
		}
	}
	s.always = *syncMode == "always"
	if !s.always {
		go s.flushEvery(200 * time.Millisecond)
	}
	ln, err := sim.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("serving %d keys on %s\n", len(s.values), *addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go s.serve(conn)
	}
}

func open(path string, repair bool) (*store, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	values := map[string]string{}
	complete := 0
	for {
		i := bytes.IndexByte(data[complete:], '\n')
		if i < 0 {
			break
		}
		if k, v, ok := strings.Cut(string(data[complete:complete+i]), "="); ok {
			values[k] = v
		}
		complete += i + 1
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if repair && complete < len(data) {
		if err := f.Truncate(int64(complete)); err != nil {
			return nil, err
		}
		if err := f.Sync(); err != nil {
			return nil, err
		}
	}
	return &store{file: f, values: values}, nil
}

func (s *store) flushEvery(d time.Duration) {
	for range time.Tick(d) {
		s.mu.Lock()
		s.file.Sync()
		s.mu.Unlock()
	}
}

func (s *store) put(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.file, "%s=%s\n", k, v); err != nil {
		return err
	}
	if s.always {
		if err := s.file.Sync(); err != nil {
			return err
		}
	}
	s.values[k] = v
	return nil
}

func (s *store) get(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[k]
	return v, ok
}

func (s *store) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fmt.Fprint(conn, s.handle(strings.Fields(line)))
	}
}

func (s *store) handle(cmd []string) string {
	switch {
	case len(cmd) == 3 && cmd[0] == "PUT":
		if err := s.put(cmd[1], cmd[2]); err != nil {
			return "ERR " + err.Error() + "\n"
		}
		return "OK\n"
	case len(cmd) == 2 && cmd[0] == "GET":
		if v, ok := s.get(cmd[1]); ok {
			return "VAL " + v + "\n"
		}
		return "NONE\n"
	}
	return "ERR bad command\n"
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
