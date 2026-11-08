package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/antithesishq/antithesis-sdk-go/random"
)

func main() {
	addr := flag.String("addr", "server:9000", "ledger address")
	entries := flag.Int("entries", 150, "number of deposits")
	flag.Parse()
	untilServed(*addr, "TOTAL")
	lifecycle.SetupComplete(map[string]any{"ledger": *addr})
	acked, unacked := 0, 0
	for range *entries {
		amount := int(random.GetRandom()%100) + 1
		reply, err := call(*addr, "ADD "+strconv.Itoa(amount))
		if err == nil && reply == "OK" {
			acked += amount
		} else {
			unacked += amount
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Sometimes(unacked > 0, "a crash interrupted a deposit", nil)
	total, _ := strconv.Atoi(untilServed(*addr, "TOTAL"))
	lifecycle.SendEvent("deposits finished", map[string]any{"total": total, "acked": acked, "unacked": unacked})
	assert.AlwaysGreaterThanOrEqualTo(total, acked, "ledger total covers every acknowledged deposit", map[string]any{"unacked": unacked})
	assert.AlwaysLessThanOrEqualTo(total, acked+unacked, "ledger total holds no deposit that was never sent", nil)
	assert.SometimesAll([]assert.NamedBool{{First: "interrupted", Second: unacked > 0}, {First: "kept", Second: total > acked}}, "an unacknowledged deposit survived a crash", nil)
	fmt.Printf("total %d, acknowledged %d\n", total, acked)
}

func call(addr, request string) (string, error) {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := fmt.Fprintln(conn, request); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line), err
}

func untilServed(addr, request string) string {
	for {
		if reply, err := call(addr, request); err == nil && reply != "ERR" {
			return reply
		}
		time.Sleep(50 * time.Millisecond)
	}
}
