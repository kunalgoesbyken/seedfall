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

const addr = "counter:7000"

func main() {
	incs := flag.Int("incs", 40, "increments the client sends")
	flag.Parse()
	value()
	retried := false
	for i := range *incs {
		for {
			if _, err := call(fmt.Sprintf("INC %d", i)); err == nil {
				break
			}
			retried = true
			time.Sleep(20 * time.Millisecond)
		}
	}
	sim.Sometimes(retried, "an increment was retried after its reply was lost")
	got := value()
	sim.Assert(got == *incs, fmt.Sprintf("counter is %d after %d increments", got, *incs))
}

func value() int {
	for {
		if got, err := call("GET"); err == nil {
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
