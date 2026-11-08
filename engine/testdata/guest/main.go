package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const addr = "store:7000"

func main() {
	role := flag.String("role", "store", "store or client")
	durable := flag.Bool("fsync", true, "fsync every write before acknowledging it")
	writes := flag.Int("writes", 60, "writes the client sends")
	forever := flag.Bool("forever", false, "keep the client running after its writes")
	record := flag.Bool("history", false, "record every write in the linearizability history")
	least := flag.Int("least", 0, "count the finally check waits for")
	within := flag.Duration("within", time.Second, "how long the finally check waits")
	flag.Parse()
	switch *role {
	case "client":
		client(*writes, *record, *forever)
	case "finally":
		sim.Eventually(func() bool {
			stored, err := call("COUNT")
			return err == nil && stored >= *least
		}, "the store answers after recovery", *within)
	default:
		store(*durable)
	}
}

func store(durable bool) {
	f, err := os.OpenFile("log", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile("log")
	if err != nil {
		panic(err)
	}
	var mu sync.Mutex
	count := bytes.Count(data, []byte("\n"))
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
			mu.Lock()
			defer mu.Unlock()
			if strings.TrimSpace(req) == "PUT" {
				if _, err := f.WriteString("x\n"); err != nil {
					sim.Sometimes(errors.Is(err, syscall.ENOSPC), "a write failed with ENOSPC")
					return
				}
				if durable && f.Sync() != nil {
					return
				}
				count++
			}
			if sim.Buggify("store drops the reply") {
				return
			}
			fmt.Fprintf(conn, "%d\n", count)
		}()
	}
}

func client(writes int, record, forever bool) {
	acked := 0
	for i := range writes {
		var op sim.Op
		if record {
			op = sim.Put(0, "k", strconv.Itoa(i))
		}
		_, err := call("PUT")
		switch {
		case err == nil:
			acked++
			if record {
				op.Ok("")
			}
		case record:
			op.Unknown()
		}
		time.Sleep(10 * time.Millisecond)
	}
	sim.Sometimes(acked < writes, "a fault interrupted the workload")
	for {
		if stored, err := call("COUNT"); err == nil && !forever {
			sim.Assert(stored >= acked, fmt.Sprintf("acknowledged writes lost: %d acknowledged, %d stored", acked, stored))
			return
		}
		time.Sleep(50 * time.Millisecond)
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
	return reply(conn)
}

func reply(conn net.Conn) (int, error) {
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(line))
}
