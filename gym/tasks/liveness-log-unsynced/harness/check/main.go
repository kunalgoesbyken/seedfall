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
	primary := flag.String("primary", "primary:7000", "primary address")
	backup := flag.String("backup", "backup:7000", "backup address")
	adds := flag.Int("adds", 40, "adds the client sent")
	within := flag.Duration("within", 5*time.Second, "how long the replicas may take to agree")
	flag.Parse()
	var p, b int
	sim.Eventually(func() bool {
		p, b = count(*primary), count(*backup)
		return p >= 0 && p == b
	}, "primary and backup report the same count", *within)
	sim.AlwaysLessThanOrEqualTo(p, *adds, "no add is applied twice")
	sim.AlwaysGreaterThanOrEqualTo(p, *adds, "no acknowledged add is lost")
	probe := fmt.Sprintf("probe-%016x", sim.Random())
	sim.Eventually(func() bool { return add(*primary, probe) }, "a new add is acknowledged", *within)
	sim.Eventually(func() bool {
		after, mirrored := count(*primary), count(*backup)
		return after > p && after == mirrored
	}, "a new add reaches both replicas", *within)
	fmt.Printf("primary and backup agree on %d\n", p)
}

func add(addr, id string) bool {
	conn, err := sim.Dial("tcp", addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := fmt.Fprintf(conn, "ADD %s\n", id); err != nil {
		return false
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	return err == nil && !strings.HasPrefix(reply, "ERR")
}

func count(addr string) int {
	conn, err := sim.Dial("tcp", addr)
	if err != nil {
		return -1
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := fmt.Fprintln(conn, "GET"); err != nil {
		return -1
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return -1
	}
	return n
}
