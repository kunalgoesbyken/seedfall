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

func main() {
	addr := flag.String("addr", "server:7000", "server address")
	writes := flag.Int("writes", 200, "number of writes")
	gap := flag.Duration("gap", 10*time.Millisecond, "pause between writes")
	flag.Parse()

	var acked []entry
	for i := range *writes {
		e := entry{fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)}
		if reply, err := call(*addr, "PUT "+e.key+" "+e.value); err == nil && reply == "OK" {
			acked = append(acked, e)
		}
		time.Sleep(*gap)
	}
	fmt.Printf("%d of %d writes acknowledged, verifying\n", len(acked), *writes)
	sim.Sometimes(len(acked) < *writes, "a crash interrupted the workload")
	for _, e := range acked {
		reply := callUntilServed(*addr, "GET "+e.key)
		sim.Assert(reply == "VAL "+e.value, fmt.Sprintf("acknowledged write lost: %s=%s, server now answers %q", e.key, e.value, reply))
	}
	fmt.Println("all acknowledged writes survived")
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

func callUntilServed(addr, request string) string {
	for {
		if reply, err := call(addr, request); err == nil {
			return reply
		}
		time.Sleep(50 * time.Millisecond)
	}
}
