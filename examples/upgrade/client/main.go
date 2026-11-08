package main

import (
	"bufio"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const addr = "ledger:7000"

func main() {
	writes := flag.Int("writes", 150, "deposits the client makes into new accounts")
	expected := flag.String("expected", "expected", "v1 ledger the node started with, to check every seeded balance against")
	flag.Parse()
	seeded := balances(*expected)
	sent, acked := map[string]int{}, map[string]int{}
	upgraded := false
	for i := range *writes {
		acct := fmt.Sprintf("new%d", i%5)
		for {
			sent[acct]++
			if _, err := call("ADD " + acct + " 1"); err == nil {
				acked[acct]++
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if i%10 == 0 {
			check(seeded)
			upgraded = upgraded || retry("VERSION "+acct) == 2
		}
	}
	check(seeded)
	for acct, n := range acked {
		got := retry("GET " + acct)
		sim.Assert(got >= n && got <= sent[acct], fmt.Sprintf("%s holds %d after %d acknowledged and %d sent deposits", acct, got, n, sent[acct]))
	}
	sim.Sometimes(upgraded, "the ledger was upgraded to v2 while the client ran")
}

func check(seeded map[string]int) {
	for _, acct := range slices.Sorted(maps.Keys(seeded)) {
		got := retry("GET " + acct)
		sim.Assert(got == seeded[acct], fmt.Sprintf("seeded account %s holds %d, want %d", acct, got, seeded[acct]))
	}
}

func balances(path string) map[string]int {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	out := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		acct, amount, _ := strings.Cut(line, " ")
		n, err := strconv.Atoi(amount)
		if err != nil {
			panic(fmt.Sprintf("%s: %q", path, line))
		}
		out[acct] += n
	}
	return out
}

func retry(req string) int {
	for {
		if got, err := call(req); err == nil {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func call(req string) (int, error) {
	conn, err := sim.Dial("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := fmt.Fprintf(conn, "%s\n", req); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(line))
}
