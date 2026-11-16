package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const readAttempts = 64

type store struct {
	mu     sync.Mutex
	file   *os.File
	values map[string]string
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data", "data directory")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	s, err := open(*dir + "/kv.log")
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range []string{*dir, filepath.Dir(*dir)} {
		if err := syncDir(d); err != nil {
			log.Fatal(err)
		}
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

func open(path string) (*store, error) {
	values, complete, size, err := load(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	sim.Sometimes(complete < size, "recovery found a torn tail")
	if complete < size {
		if err := f.Truncate(int64(complete)); err != nil {
			return nil, err
		}
		if err := f.Sync(); err != nil {
			return nil, err
		}
	}
	return &store{file: f, values: values}, nil
}

func load(path string) (map[string]string, int, int, error) {
	tails := map[string]bool{}
	for range readAttempts {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, 0, 0, err
		}
		values, complete, ok := parse(data)
		if !ok {
			continue
		}
		tail := string(data[complete:])
		if tails[tail] {
			return values, complete, len(data), nil
		}
		tails[tail] = true
	}
	return nil, 0, 0, fmt.Errorf("%s: no two reads agree on a log that passes its checksums in %d attempts", path, readAttempts)
}

func parse(data []byte) (map[string]string, int, bool) {
	values := map[string]string{}
	complete := 0
	for {
		i := bytes.IndexByte(data[complete:], '\n')
		if i < 0 {
			return values, complete, true
		}
		record, ok := verify(string(data[complete : complete+i]))
		if !ok {
			return nil, 0, false
		}
		if k, v, ok := strings.Cut(record, "="); ok {
			values[k] = v
		}
		complete += i + 1
	}
}

func verify(line string) (string, bool) {
	record, sum, ok := strings.Cut(line, "#")
	return record, ok && sum == checksum(record)
}

func checksum(record string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(record)))
}

func encode(k, v string) string {
	record := k + "=" + v
	return record + "#" + checksum(record) + "\n"
}

func (s *store) put(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := s.file.WriteString(encode(k, v)); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return err
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
