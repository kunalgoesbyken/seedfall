package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type window struct {
	mu    sync.Mutex
	ok    int
	total int
}

func (w *window) add(ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.total++
	if ok {
		w.ok++
	}
}

func (w *window) drain() (ok, total int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ok, total = w.ok, w.total
	w.ok, w.total = 0, 0
	return ok, total
}

func main() {
	addrs := flag.String("nodes", "n1:8000,n2:8000,n3:8000", "node API addresses")
	clients := flag.Int("clients", 3, "concurrent clients")
	keys := flag.Int("keys", 4, "distinct keys")
	span := flag.Duration("span", 12*time.Second, "how long the clients run, which is the operator's deadline")
	every := flag.Duration("window", 2*time.Second, "length of one availability window")
	slo := flag.Float64("slo", 0.4, "share of client operations that must succeed in every window after the first")
	version := flag.String("version", "2", "version every node must report by the deadline")
	grace := flag.Duration("grace", 3*time.Second, "how long after the deadline the nodes may take to answer")
	flag.Parse()
	nodes := strings.Split(*addrs, ",")

	var tally window
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for c := range *clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run(c, nodes, *keys, &tally, stop)
		}()
	}
	watch(&tally, *span, *every, *slo)
	close(stop)
	wg.Wait()
	rolled(nodes, *version, *grace)
	fmt.Println("workload done, checking linearizability")
}

func watch(tally *window, span, every time.Duration, slo float64) {
	for i := 0; time.Duration(i)*every < span; i++ {
		time.Sleep(every)
		ok, total := tally.drain()
		fmt.Printf("window %d: %d of %d operations succeeded\n", i, ok, total)
		if i > 0 && total > 0 {
			sim.Always(float64(ok)/float64(total) >= slo, "client availability meets the SLO in every window", fmt.Sprintf("window %d: %d of %d operations succeeded", i, ok, total))
		}
	}
}

func rolled(nodes []string, version string, grace time.Duration) {
	deadline := time.Now().Add(grace)
	var behind []string
	for {
		behind = behind[:0]
		for _, n := range nodes {
			if reply, err := call(n, "VERSION"); err != nil || reply != "VER "+version {
				behind = append(behind, n)
			}
		}
		if len(behind) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	sim.Always(len(behind) == 0, "every node runs the new version by the deadline", fmt.Sprintf("nodes still on the old version: %v", behind))
}

func run(client int, nodes []string, keys int, tally *window, stop <-chan struct{}) {
	leader := client % len(nodes)
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		default:
		}
		key := fmt.Sprintf("k%d", rand.IntN(keys))
		if rand.IntN(2) == 0 {
			value := fmt.Sprintf("c%d-%d", client, i)
			leader = record(sim.Put(client, key, value), nodes, leader, "PUT "+key+" "+value, tally)
		} else {
			leader = record(sim.Get(client, key), nodes, leader, "GET "+key, tally)
		}
		time.Sleep(time.Duration(10+rand.IntN(30)) * time.Millisecond)
	}
}

func record(op sim.Op, nodes []string, leader int, request string, tally *window) int {
	reply, err := call(nodes[leader], request)
	switch {
	case err != nil || strings.HasPrefix(reply, "ERR"):
		op.Unknown()
		tally.add(false)
	case reply == "NOTLEADER":
		op.Failed()
		tally.add(false)
		return (leader + 1) % len(nodes)
	case reply == "OK", reply == "NONE":
		op.Ok("")
		tally.add(true)
	case strings.HasPrefix(reply, "VAL "):
		op.Ok(strings.TrimPrefix(reply, "VAL "))
		tally.add(true)
	default:
		op.Unknown()
		tally.add(false)
	}
	if err != nil {
		return (leader + 1) % len(nodes)
	}
	return leader
}

func call(addr, request string) (string, error) {
	conn, err := sim.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := fmt.Fprintf(conn, "%s\n", request); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line), err
}
