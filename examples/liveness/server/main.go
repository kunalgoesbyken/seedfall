package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const logFile = "applied.log"

type store struct {
	mu      sync.Mutex
	log     *os.File
	applied map[string]bool
	value   int
	dedupe  bool
	backup  *replica
}

type replica struct {
	addr   string
	redial bool
	conn   net.Conn
	reader *bufio.Reader
	broken bool
}

var errBackupDown = errors.New("backup connection lost")

func main() {
	backup := flag.String("backup", "", "address of the backup; empty makes this node the backup")
	dedupe := flag.Bool("dedupe", false, "remember applied request ids so a retried add is applied once")
	redial := flag.Bool("redial", false, "reconnect to the backup after the replication connection fails")
	timeout := flag.Duration("timeout", 500*time.Millisecond, "how long the primary waits for the backup")
	flag.Parse()
	s := open(*dedupe)
	if *backup != "" {
		s.backup = &replica{addr: *backup, redial: *redial}
		s.backup.connect(*timeout)
	}
	l, err := sim.Listen("tcp", ":7000")
	if err != nil {
		panic(err)
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go s.serve(conn, *timeout)
	}
}

func open(dedupe bool) *store {
	data, err := os.ReadFile(logFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		panic(err)
	}
	s := &store{applied: map[string]bool{}, dedupe: dedupe}
	for _, id := range strings.Fields(string(data)) {
		s.applied[id] = true
		s.value++
	}
	if s.log, err = os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
		panic(err)
	}
	return s
}

func (s *store) serve(conn net.Conn, timeout time.Duration) {
	defer conn.Close()
	lines := bufio.NewScanner(conn)
	for lines.Scan() {
		cmd, id, _ := strings.Cut(strings.TrimSpace(lines.Text()), " ")
		if _, err := fmt.Fprintf(conn, "%s\n", s.handle(cmd, id, timeout)); err != nil {
			return
		}
	}
}

func (s *store) handle(cmd, id string, timeout time.Duration) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cmd != "ADD" {
		return fmt.Sprint(s.value)
	}
	if s.dedupe && s.applied[id] {
		return fmt.Sprint(s.value)
	}
	if s.backup != nil {
		if err := s.backup.forward(id, timeout); err != nil {
			return "ERR " + err.Error()
		}
	}
	if _, err := s.log.WriteString(id + "\n"); err != nil {
		return "ERR " + err.Error()
	}
	if err := s.log.Sync(); err != nil {
		return "ERR " + err.Error()
	}
	s.applied[id] = true
	s.value++
	return fmt.Sprint(s.value)
}

func (r *replica) connect(timeout time.Duration) {
	for {
		conn, err := sim.Dial("tcp", r.addr)
		if err == nil {
			r.conn, r.reader = conn, bufio.NewReader(conn)
			return
		}
		time.Sleep(timeout)
	}
}

func (r *replica) forward(id string, timeout time.Duration) error {
	if r.broken && !r.redial {
		return errBackupDown
	}
	if r.broken {
		r.connect(timeout)
		r.broken = false
	}
	r.conn.SetDeadline(time.Now().Add(timeout))
	_, err := fmt.Fprintf(r.conn, "ADD %s\n", id)
	var reply string
	if err == nil {
		reply, err = r.reader.ReadString('\n')
	}
	if err == nil && strings.HasPrefix(reply, "ERR") {
		err = errors.New(strings.TrimSpace(reply))
	}
	if err != nil {
		r.conn.Close()
		r.broken = true
	}
	return err
}
