package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const deleted = "NONE"

type history struct {
	acked     string
	ambiguous []string
}

func (h history) allows(got string) bool {
	return got == h.acked || slices.Contains(h.ambiguous, got)
}

func main() {
	addr := flag.String("addr", "server:7000", "server address")
	ops := flag.Int("ops", 400, "number of operations")
	keys := flag.Int("keys", 20, "distinct keys")
	size := flag.Int("size", 512, "value size in bytes")
	deletes := flag.Float64("deletes", 0.2, "fraction of operations that are deletes")
	gap := flag.Duration("gap", 10*time.Millisecond, "pause between operations")
	outage := flag.Duration("outage", 10*time.Second, "longest the store may refuse to serve before the run fails")
	flag.Parse()

	r := rand.New(rand.NewPCG(1, 2))
	state := map[string]*history{}
	for i := range *keys {
		state[key(i)] = &history{acked: deleted}
	}
	for i := range *ops {
		k := key(r.IntN(*keys))
		want, request := value(i, *size), ""
		if r.Float64() < *deletes {
			want, request = deleted, "DEL "+k
		} else {
			request = "PUT " + k + " " + want
		}
		h := state[k]
		if reply, err := call(*addr, request); err == nil && reply == "OK" {
			*h = history{acked: want}
		} else {
			h.ambiguous = append(h.ambiguous, want)
		}
		time.Sleep(*gap)
	}
	fmt.Println("workload done, verifying every key")
	for i := range *keys {
		k := key(i)
		got := strings.TrimPrefix(callUntilServed(*addr, "GET "+k, *outage), "VAL ")
		if !state[k].allows(got) {
			sim.Fail(fmt.Sprintf("key %s: want %s (or an unacknowledged later write), store answers %s", k, short(state[k].acked), short(got)))
		}
	}
	fmt.Println("every key matches its last acknowledged operation")
}

func key(i int) string { return fmt.Sprintf("k%02d", i) }

func value(i, size int) string {
	head := fmt.Sprintf("v%d-", i)
	return head + strings.Repeat("x", max(size-len(head), 0))
}

func short(v string) string {
	head, _, _ := strings.Cut(v, "-")
	return head
}

func call(addr, request string) (string, error) {
	conn, err := sim.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := fmt.Fprintf(conn, "%s\n", request); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line), err
}

func callUntilServed(addr, request string, outage time.Duration) string {
	deadline := time.Now().Add(outage)
	for {
		reply, err := call(addr, request)
		if err == nil && !strings.HasPrefix(reply, "ERR") {
			return reply
		}
		if time.Now().After(deadline) {
			sim.Fail(fmt.Sprintf("store unavailable for %s: %q answers %q (%v)", outage, request, reply, err))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
