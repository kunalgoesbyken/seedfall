package main

import (
	"bufio"
	"bytes"
	"errors"
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

const readAttempts = 40

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
	var data []byte
	var values map[string]string
	var complete int
	for attempt := 1; ; attempt++ {
		var err error
		data, err = os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		var bad, tail bool
		values, complete, bad, tail = parse(data)
		if !bad || attempt == readAttempts && tail {
			break
		}
		if attempt == readAttempts {
			return nil, fmt.Errorf("%s: the record at offset %d fails its checksum in %d reads", path, complete, attempt)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	sim.Sometimes(complete < len(data), "recovery found a torn tail")
	if complete < len(data) {
		if err := f.Truncate(int64(complete)); err != nil {
			return nil, err
		}
		if err := f.Sync(); err != nil {
			return nil, err
		}
	}
	return &store{file: f, values: values}, nil
}

func parse(data []byte) (map[string]string, int, bool, bool) {
	values := map[string]string{}
	complete := 0
	for {
		i := bytes.IndexByte(data[complete:], '\n')
		if i < 0 {
			return values, complete, false, false
		}
		record, ok := verify(string(data[complete : complete+i]))
		if !ok {
			return values, complete, true, complete+i+1 == len(data)
		}
		if k, v, ok := strings.Cut(record, "="); ok {
			values[k] = v
		}
		complete += i + 1
	}
}

func verify(line string) (string, bool) {
	record, sum, ok := strings.Cut(line, "#")
	return record, ok && sum == fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(record)))
}

func (s *store) encode(k, v string) string {
	record := k + "=" + v
	record += fmt.Sprintf("#%08x", crc32.ChecksumIEEE([]byte(record)))
	return record + "\n"
}

func (s *store) put(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := s.file.WriteString(s.encode(k, v)); err != nil {
		return err
	}
	s.file.Sync()
	s.values[k] = v
	return nil
}

func (s *store) failedSync(end int64, err error) error {
	return errors.Join(err, s.file.Truncate(end))
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
	if len(cmd) > 0 && cmd[0] == "PUT" {
		return "ERR writes are disabled\n"
	}
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
