package main

import (
	"bufio"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type entry struct{ key, value string }

type cluster struct {
	nodes  []string
	leader int
}

func main() {
	nodes := flag.String("nodes", "n1:8000,n2:8000,n3:8000", "node API addresses")
	writes := flag.Int("writes", 150, "number of writes")
	gap := flag.Duration("gap", 20*time.Millisecond, "pause between writes")
	flag.Parse()
	c := &cluster{nodes: strings.Split(*nodes, ",")}

	var acked []entry
	for i := range *writes {
		e := entry{fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)}
		if c.tryLeader("PUT "+e.key+" "+e.value, 3) == "OK" {
			acked = append(acked, e)
		}
		time.Sleep(*gap)
	}
	fmt.Printf("%d of %d writes acknowledged, verifying\n", len(acked), *writes)
	for _, e := range acked {
		reply := c.untilServed("GET " + e.key)
		sim.Assert(reply == "VAL "+e.value, fmt.Sprintf("acknowledged write lost: %s=%s, leader answers %q", e.key, e.value, reply))
	}
	fmt.Println("all acknowledged writes are readable from the leader")
}

func (c *cluster) tryLeader(request string, rounds int) string {
	for range rounds * len(c.nodes) {
		reply, err := call(c.nodes[c.leader], request)
		if err == nil && reply != "NOTLEADER" {
			return reply
		}
		c.leader = (c.leader + 1) % len(c.nodes)
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

func (c *cluster) untilServed(request string) string {
	for {
		reply := c.tryLeader(request, 1)
		if reply != "" && !strings.HasPrefix(reply, "ERR") {
			return reply
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func call(addr, request string) (string, error) {
	conn, err := sim.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(conn, "%s\n", request); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line), err
}
