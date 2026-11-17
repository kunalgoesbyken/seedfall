package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
)

func main() {
	flag.String("backup", "", "")
	flag.Parse()
	l, err := sim.Listen("tcp", ":7000")
	if err != nil {
		panic(err)
	}
	for {
		c, err := l.Accept()
		if err != nil {
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				if strings.HasPrefix(sc.Text(), "ADD") {
					fmt.Fprintln(c, 1)
				} else {
					fmt.Fprintln(c, 40)
				}
			}
		}(c)
	}
}
