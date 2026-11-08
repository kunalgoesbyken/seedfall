package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"hash/crc32"
	"log"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/blevesearch/mmap-go"
	"github.com/gofrs/flock"
	"github.com/kunalgoesbyken/seedfall/sim"
)

const (
	pageSize        = 4096
	dataPages       = 16
	checkpointEvery = 8
	lengthSize      = 2
)

type store struct {
	mu      sync.Mutex
	mapped  mmap.MMap
	wal     *os.File
	pending int
}

func main() {
	addr := flag.String("addr", ":7000", "listen address")
	dir := flag.String("dir", "data", "data directory")
	lenient := flag.Bool("lenient", false, "treat a page that differs from its checkpoint checksum as dirty, relying on the write-ahead log")
	flag.Parse()
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	lock := flock.New(filepath.Join(*dir, "LOCK"))
	locked, err := lock.TryLock()
	sim.Always(err == nil && locked, "the store takes its lock file", fmt.Sprint(err))
	s, err := open(*dir, *lenient)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := sim.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("serving on %s\n", *addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go s.serve(conn)
	}
}

func open(dir string, lenient bool) (*store, error) {
	f, err := os.OpenFile(filepath.Join(dir, "pages"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := int64((dataPages + 1) * pageSize)
	fresh := info.Size() < size
	if fresh {
		if err := f.Truncate(size); err != nil {
			return nil, err
		}
	}
	mapped, err := mmap.MapRegion(f, int(size), mmap.RDWR, 0, 0)
	if err != nil {
		return nil, err
	}
	wal, err := os.OpenFile(filepath.Join(dir, "wal"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s := &store{mapped: mapped, wal: wal}
	if fresh {
		if err := s.checkpoint(); err != nil {
			return nil, err
		}
	} else {
		for page := 0; page < dataPages; page++ {
			sim.Always(lenient || s.checksum(page) == s.savedChecksum(page), "every page matches its checkpoint checksum or the log covers it", fmt.Sprintf("page %d", page))
		}
	}
	return s, s.replay()
}

func (s *store) page(i int) []byte {
	return s.mapped[pageSize*(i+1) : pageSize*(i+2)]
}

func (s *store) checksum(i int) uint32 { return crc32.ChecksumIEEE(s.page(i)) }

func (s *store) savedChecksum(i int) uint32 {
	return binary.LittleEndian.Uint32(s.mapped[i*crc32.Size:])
}

func (s *store) checkpoint() error {
	for i := range dataPages {
		binary.LittleEndian.PutUint32(s.mapped[i*crc32.Size:], s.checksum(i))
	}
	if err := s.mapped.Flush(); err != nil {
		return err
	}
	if err := s.wal.Truncate(0); err != nil {
		return err
	}
	s.pending = 0
	return s.wal.Sync()
}

func (s *store) replay() error {
	data, err := os.ReadFile(s.wal.Name())
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			s.apply(k, v)
			s.pending++
		}
	}
	return nil
}

func pageOf(key string) int { return int(crc32.ChecksumIEEE([]byte(key)) % dataPages) }

func decode(page []byte) map[string]string {
	n := binary.LittleEndian.Uint16(page)
	entries := map[string]string{}
	for _, line := range strings.Split(string(page[lengthSize:lengthSize+int(n)]), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			entries[k] = v
		}
	}
	return entries
}

func (s *store) apply(k, v string) {
	page := s.page(pageOf(k))
	entries := decode(page)
	entries[k] = v
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(entries)) {
		fmt.Fprintf(&b, "%s=%s\n", key, entries[key])
	}
	binary.LittleEndian.PutUint16(page, uint16(b.Len()))
	copy(page[lengthSize:], b.String())
}

func (s *store) put(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.wal, "%s=%s\n", k, v); err != nil {
		return err
	}
	if err := s.wal.Sync(); err != nil {
		return err
	}
	s.apply(k, v)
	s.pending++
	if s.pending >= checkpointEvery {
		return s.checkpoint()
	}
	return nil
}

func (s *store) get(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := decode(s.page(pageOf(k)))[k]
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
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "PUT":
			if err := s.put(fields[1], fields[2]); err != nil {
				fmt.Fprintf(conn, "ERR %v\n", err)
				continue
			}
			fmt.Fprint(conn, "OK\n")
		case len(fields) == 2 && fields[0] == "GET":
			if v, ok := s.get(fields[1]); ok {
				fmt.Fprintf(conn, "VAL %s\n", v)
			} else {
				fmt.Fprint(conn, "MISS\n")
			}
		default:
			fmt.Fprint(conn, "ERR bad request\n")
		}
	}
}
