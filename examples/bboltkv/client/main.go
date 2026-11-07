package main

import (
	"bufio"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

func main() {
	addr := flag.String("addr", "server:7000", "server address")
	writes := flag.Int("writes", 300, "number of writes")
	keys := flag.Int("keys", 0, "distinct keys, 0 means every write uses a new key")
	gap := flag.Duration("gap", 10*time.Millisecond, "pause between writes")
	flag.Parse()

	acked := map[string]int{}
	var order []string
	for i := range *writes {
		key := keyFor(i, *keys)
		if reply, err := call(*addr, fmt.Sprintf("SET %s v%d", key, i)); err == nil && reply == "OK" {
			if _, seen := acked[key]; !seen {
				order = append(order, key)
			}
			acked[key] = i
		}
		time.Sleep(*gap)
	}
	fmt.Printf("%d keys acknowledged, verifying\n", len(order))
	for _, key := range order {
		reply := callUntilServed(*addr, "GET "+key)
		if version(reply) < acked[key] {
			sim.Fail(fmt.Sprintf("acknowledged write lost: %s=v%d, server now answers %q", key, acked[key], reply))
		}
	}
	fmt.Println("all acknowledged writes survived")
}

func version(reply string) int {
	v, err := strconv.Atoi(strings.TrimPrefix(reply, "VAL v"))
	if err != nil {
		return -1
	}
	return v
}

func keyFor(i, keys int) string {
	if keys > 0 {
		i %= keys
	}
	return fmt.Sprintf("k%d", i)
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
