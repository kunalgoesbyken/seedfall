package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
)

func main() {
	ln, err := sim.Listen("tcp", ":8000")
	if err != nil {
		panic(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go serve(conn)
	}
}

func serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 3 && f[0] == "PUT":
			fmt.Fprint(conn, "OK\n")
		case len(f) == 2 && f[0] == "GET":
			fmt.Fprintf(conn, "VAL v%s\n", f[1][1:])
		}
	}
}
