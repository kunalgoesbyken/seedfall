package logserver

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type Appender interface {
	Append(data []byte) error
}

type server struct {
	mu      sync.Mutex
	log     Appender
	entries [][]byte
}

func Serve(addr string, log Appender, recovered [][]byte) error {
	ln, err := sim.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Printf("recovered %d entries\n", len(recovered))
	s := &server{log: log, entries: recovered}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.serve(conn)
	}
}

func SyncDirs(dir string) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if err := syncDir(d); err != nil {
			log.Fatal(err)
		}
		if d == filepath.Dir(d) {
			return
		}
	}
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *server) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fmt.Fprintln(conn, s.handle(strings.Fields(line)))
	}
}

func (s *server) handle(cmd []string) string {
	if len(cmd) != 2 {
		return "ERR bad command"
	}
	switch cmd[0] {
	case "APPEND":
		return s.append([]byte(cmd[1]))
	case "READ":
		return s.read(cmd[1])
	}
	return "ERR bad command"
}

func (s *server) append(data []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.log.Append(data); err != nil {
		return "ERR " + err.Error()
	}
	s.entries = append(s.entries, data)
	return "OK " + strconv.Itoa(len(s.entries))
}

func (s *server) read(arg string) string {
	i, err := strconv.Atoi(arg)
	if err != nil {
		return "ERR bad index"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 1 || i > len(s.entries) {
		return "NONE"
	}
	return "VAL " + string(s.entries[i-1])
}
