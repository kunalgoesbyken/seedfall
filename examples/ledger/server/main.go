package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

type ledger struct {
	mu    sync.Mutex
	file  *os.File
	total int
	sync  bool
}

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	path := flag.String("ledger", "data/ledger.log", "ledger file")
	syncWrites := flag.Bool("fsync", true, "fsync every entry before acknowledging it")
	flag.Parse()
	l, err := open(*path, *syncWrites)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ledger total %d\n", l.total)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go l.serve(conn)
	}
}

func open(path string, syncWrites bool) (*ledger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	total, complete := replay(data)
	assert.Sometimes(complete < len(data), "recovery dropped a partial entry", map[string]any{"bytes": len(data) - complete})
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(int64(complete)); err != nil {
		return nil, err
	}
	if _, err := f.Seek(int64(complete), 0); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return &ledger{file: f, total: total, sync: syncWrites}, nil
}

func replay(data []byte) (int, int) {
	total, complete := 0, 0
	for {
		i := strings.IndexByte(string(data[complete:]), '\n')
		if i < 0 {
			return total, complete
		}
		n, err := strconv.Atoi(string(data[complete : complete+i]))
		if err != nil {
			return total, complete
		}
		total += n
		complete += i + 1
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

func (l *ledger) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fmt.Fprintln(conn, l.handle(strings.Fields(line)))
	}
}

func (l *ledger) handle(cmd []string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(cmd) == 1 && cmd[0] == "TOTAL" {
		return strconv.Itoa(l.total)
	}
	if len(cmd) != 2 || cmd[0] != "ADD" {
		return "ERR"
	}
	n, err := strconv.Atoi(cmd[1])
	if err != nil {
		return "ERR"
	}
	if _, err := fmt.Fprintf(l.file, "%d\n", n); err != nil {
		return "ERR"
	}
	if l.sync {
		if err := l.file.Sync(); err != nil {
			return "ERR"
		}
	}
	l.total += n
	return "OK"
}
