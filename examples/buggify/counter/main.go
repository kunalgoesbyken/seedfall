package main

import (
	"bufio"
	"flag"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type counter struct {
	mu      sync.Mutex
	value   int
	applied map[string]int
	dedupe  bool
}

func (c *counter) inc(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.applied[id]; ok && c.dedupe {
		return v
	}
	c.value++
	c.applied[id] = c.value
	return c.value
}

func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

var delays = []time.Duration{time.Millisecond, 5 * time.Millisecond, 50 * time.Millisecond}

func main() {
	dedupe := flag.Bool("dedupe", false, "remember applied request ids so a retried increment is applied once")
	flag.Parse()
	c := &counter{applied: map[string]int{}, dedupe: *dedupe}
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
			cmd, id, _ := strings.Cut(strings.TrimSpace(req), " ")
			if cmd != "INC" {
				fmt.Fprintf(conn, "%d\n", c.get())
				return
			}
			if sim.BuggifyWithProb("slow apply", 0.5) {
				time.Sleep(sim.RandomChoice(delays))
			}
			v := c.inc(id)
			if sim.Buggify("reply lost after apply") {
				return
			}
			fmt.Fprintf(conn, "%d\n", v)
		}()
	}
}
