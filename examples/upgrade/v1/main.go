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

const ledger = "data/ledger"

func main() {
	balances := map[string]int{}
	data := recoverTail(ledger)
	for _, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
		acct, amount, ok := strings.Cut(line, " ")
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
			if _, err := fmt.Fprintf(f, "%s %d\n", acct, n); err != nil {
				panic(err)
			}
			if err := f.Sync(); err != nil {
				panic(err)
			}
			balances[acct] += n
			return strconv.Itoa(balances[acct])
		case "VERSION":
			return "1"
		}
		return strconv.Itoa(balances[acct])
	})
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
