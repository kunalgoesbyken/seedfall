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

type entry struct {
	index   int
	payload string
}

func main() {
	addr := flag.String("addr", "server:7000", "server address")
	appends := flag.Int("appends", 200, "number of appends")
	minSize := flag.Int("min", 8, "minimum payload size in bytes")
	maxSize := flag.Int("max", 120, "maximum payload size in bytes")
	gap := flag.Duration("gap", 10*time.Millisecond, "pause between appends")
	flag.Parse()

	var acked []entry
	for i := range *appends {
		p := payload(i, *minSize+(i*37)%(*maxSize-*minSize+1))
		if index, ok := appendEntry(*addr, p); ok {
			acked = append(acked, entry{index, p})
		}
		time.Sleep(*gap)
	}
	fmt.Printf("%d of %d appends acknowledged, verifying\n", len(acked), *appends)
	for _, e := range acked {
		reply := callUntilServed(*addr, "READ "+strconv.Itoa(e.index))
		sim.Assert(reply == "VAL "+e.payload, fmt.Sprintf("acknowledged entry %d lost: server now answers %q", e.index, truncate(reply)))
	}
	verifyNoGarbage(*addr, *minSize, *maxSize)
	fmt.Println("all acknowledged entries survived, no garbage entries")
}

func verifyNoGarbage(addr string, minSize, maxSize int) {
	for index := 1; ; index++ {
		reply := callUntilServed(addr, "READ "+strconv.Itoa(index))
		if reply == "NONE" {
			return
		}
		data, _ := strings.CutPrefix(reply, "VAL ")
		sim.Assert(wellFormed(data, minSize, maxSize), fmt.Sprintf("entry %d was never written by the client: %q", index, truncate(data)))
	}
}

func wellFormed(data string, minSize, maxSize int) bool {
	var i int
	if _, err := fmt.Sscanf(data, "e%d-", &i); err != nil || i < 0 {
		return false
	}
	return data == payload(i, minSize+(i*37)%(maxSize-minSize+1))
}

func payload(i, size int) string {
	p := fmt.Sprintf("e%d-", i)
	return p + strings.Repeat(strconv.Itoa(i%10), max(size-len(p), 0))
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func appendEntry(addr, p string) (int, bool) {
	reply, err := call(addr, "APPEND "+p)
	if err != nil {
		return 0, false
	}
	n, ok := strings.CutPrefix(reply, "OK ")
	if !ok {
		return 0, false
	}
	index, err := strconv.Atoi(n)
	return index, err == nil
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
