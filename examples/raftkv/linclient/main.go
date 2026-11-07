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

func main() {
	addrs := flag.String("nodes", "n1:8000,n2:8000,n3:8000", "node API addresses")
	clients := flag.Int("clients", 4, "concurrent clients")
	ops := flag.Int("ops", 60, "operations per client")
	keys := flag.Int("keys", 4, "distinct keys")
	flag.Parse()
	nodes := strings.Split(*addrs, ",")

	var wg sync.WaitGroup
	for c := range *clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run(c, nodes, *ops, *keys)
		}()
	}
	wg.Wait()
	fmt.Println("workload done, checking linearizability")
}

func run(client int, nodes []string, ops, keys int) {
	leader := client % len(nodes)
	for i := range ops {
		key := fmt.Sprintf("k%d", rand.IntN(keys))
		if rand.IntN(2) == 0 {
			value := fmt.Sprintf("c%d-%d", client, i)
			leader = record(sim.Put(client, key, value), nodes, leader, "PUT "+key+" "+value)
		} else {
			leader = record(sim.Get(client, key), nodes, leader, "GET "+key)
		}
		time.Sleep(time.Duration(5+rand.IntN(30)) * time.Millisecond)
	}
}

func record(op sim.Op, nodes []string, leader int, request string) int {
	reply, err := call(nodes[leader], request)
	switch {
	case err != nil || strings.HasPrefix(reply, "ERR"):
		op.Unknown()
	case reply == "NOTLEADER":
		op.Failed()
		return (leader + 1) % len(nodes)
	case reply == "OK", reply == "NONE":
		op.Ok("")
	case strings.HasPrefix(reply, "VAL "):
		op.Ok(strings.TrimPrefix(reply, "VAL "))
	default:
		op.Unknown()
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
