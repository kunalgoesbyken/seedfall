package main

import (
	"bufio"
	"encoding/binary"
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

const (
	metaSize = 4096
	slotSize = 20
)

type store struct {
	mu       sync.Mutex
	file     *os.File
	values   map[string]string
	seq      uint64
	size     int64
	syncBody bool
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data", "data directory")
	syncBody := flag.Bool("sync-body", false, "fsync the record before writing the meta slot that commits it")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	s, err := open(*dir + "/kv.db")
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range []string{*dir, filepath.Dir(*dir)} {
		if err := syncDir(d); err != nil {
			log.Fatal(err)
		}
	}
	s.syncBody = *syncBody
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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	s := &store{file: f, values: map[string]string{}}
	s.seq, s.size = latestSlot(data)
	records := data[min(len(data), metaSize):]
	intact := int64(len(records)) >= s.size
	committed := string(records[:min(int64(len(records)), s.size)])
	for _, line := range strings.SplitAfter(committed, "\n") {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		intact = intact && ok && strings.HasSuffix(line, "\n") && !strings.ContainsRune(line, 0)
		s.values[k] = v
	}
	sim.Sometimes(int64(len(records)) > s.size, "recovery found a record that its meta slot does not commit")
	sim.Always(intact, "every committed record is intact", fmt.Sprintf("meta slot %d commits %d bytes of records, %d on disk, ending in %q", s.seq, s.size, len(records), committed[max(0, len(committed)-24):]))
	return s, nil
}

func latestSlot(data []byte) (uint64, int64) {
	var seq uint64
	var size int64
	for off := 0; off+slotSize <= min(len(data), 2*slotSize); off += slotSize {
		slot := data[off : off+slotSize]
		if crc32.ChecksumIEEE(slot[:16]) != binary.LittleEndian.Uint32(slot[16:]) {
			continue
		}
		if n := binary.LittleEndian.Uint64(slot); n > seq {
			seq, size = n, int64(binary.LittleEndian.Uint64(slot[8:]))
		}
	}
	return seq, size
}

func encodeSlot(seq uint64, size int64) []byte {
	slot := make([]byte, slotSize)
	binary.LittleEndian.PutUint64(slot, seq)
	binary.LittleEndian.PutUint64(slot[8:], uint64(size))
	binary.LittleEndian.PutUint32(slot[16:], crc32.ChecksumIEEE(slot[:16]))
	return slot
}

func (s *store) put(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := fmt.Sprintf("%s=%s\n", k, v)
	if _, err := s.file.WriteAt([]byte(rec), metaSize+s.size); err != nil {
		return err
	}
	if s.syncBody {
		if err := s.file.Sync(); err != nil {
			return err
		}
	}
	seq, size := s.seq+1, s.size+int64(len(rec))
	if _, err := s.file.WriteAt(encodeSlot(seq, size), int64(seq%2)*slotSize); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	s.seq, s.size, s.values[k] = seq, size, v
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
