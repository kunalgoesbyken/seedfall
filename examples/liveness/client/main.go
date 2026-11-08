package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const progressFile = "progress"

func main() {
	addr := flag.String("addr", "primary:7000", "primary address")
	adds := flag.Int("adds", 40, "adds the client sends")
	gap := flag.Duration("gap", 100*time.Millisecond, "pause between adds")
	retry := flag.Duration("retry", 20*time.Millisecond, "pause before retrying an add")
	flag.Parse()
	done := progress()
	sim.Sometimes(done > 0, "the client resumed after a crash")
	for i := done; i < *adds; i++ {
		id := fmt.Sprintf("%s-%d", sim.Node(), i)
		for !acked(*addr, id) {
			time.Sleep(*retry)
		}
		save(i + 1)
		time.Sleep(*gap)
	}
	fmt.Printf("all %d adds acknowledged\n", *adds)
}

func acked(addr, id string) bool {
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

func progress() int {
	data, err := os.ReadFile(progressFile)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		panic(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		panic(err)
	}
	return n
}

func save(n int) {
	tmp := progressFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		panic(err)
	}
	if _, err := fmt.Fprintln(f, n); err != nil {
		panic(err)
	}
	if err := f.Sync(); err != nil {
		panic(err)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
	if err := os.Rename(tmp, progressFile); err != nil {
		panic(err)
	}
}
