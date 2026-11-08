package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const (
	ledger = "data/ledger"
	header = "LEDGER v2"
)

func main() {
	data := recoverTail(ledger)
	if !strings.HasPrefix(data, header+"\n") {
		data = migrate(data, setting("etc/ledger.conf", "migrate"))
	}
	balances := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
		acct, amount, ok := strings.Cut(line, "=")
		if n, err := strconv.Atoi(amount); ok && err == nil {
			balances[acct] += n
		}
	}
	f, err := os.OpenFile(ledger, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		panic(err)
	}
	var mu sync.Mutex
	serve(func(cmd, acct string, n int) string {
		mu.Lock()
		defer mu.Unlock()
		switch cmd {
		case "ADD":
			if _, err := fmt.Fprintf(f, "%s=%d\n", acct, n); err != nil {
				panic(err)
			}
			if err := f.Sync(); err != nil {
				panic(err)
			}
			balances[acct] += n
			return strconv.Itoa(balances[acct])
		case "VERSION":
			return "2"
		}
		return strconv.Itoa(balances[acct])
	})
}

func migrate(v1, mode string) string {
	lines := []string{header}
	if mode != "none" {
		for _, line := range strings.Split(strings.TrimSuffix(v1, "\n"), "\n") {
			if acct, amount, ok := strings.Cut(line, " "); ok {
				lines = append(lines, acct+"="+amount)
			}
		}
	}
	switch mode {
	case "atomic":
		write(ledger+".tmp", lines, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
		if err := os.Rename(ledger+".tmp", ledger); err != nil {
			panic(err)
		}
		syncDir("data")
	default:
		write(ledger, lines, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	}
	return strings.Join(lines, "\n") + "\n"
}

func write(path string, lines []string, flag int) {
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			panic(err)
		}
	}
	if err := f.Sync(); err != nil {
		panic(err)
	}
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		panic(err)
	}
	defer d.Close()
	d.Sync()
}

func setting(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	panic(fmt.Sprintf("%s has no %s setting", path, key))
}

func recoverTail(path string) string {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll("data", 0o755); err != nil {
			panic(err)
		}
		return ""
	}
	if err != nil {
		panic(err)
	}
	keep := strings.LastIndexByte(string(b), '\n') + 1
	if keep < len(b) {
		if err := os.Truncate(path, int64(keep)); err != nil {
			panic(err)
		}
	}
	return string(b[:keep])
}

func serve(handle func(cmd, acct string, n int) string) {
	l, err := sim.Listen("tcp", ":7000")
	if err != nil {
		panic(err)
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer conn.Close()
			req, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				return
			}
			var cmd, acct string
			var n int
			fmt.Sscan(req, &cmd, &acct, &n)
			fmt.Fprintf(conn, "%s\n", handle(cmd, acct, n))
		}()
	}
}
