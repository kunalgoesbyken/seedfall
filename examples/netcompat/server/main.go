package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const (
	controlAddr  = ":7000"
	packetAddr   = ":7100"
	maxDatagram  = 1500
	ephemeralAny = ":0"
)

var (
	listen       = net.Listen
	listenPacket = net.ListenPacket
)

func main() {
	api := flag.String("api", "net", "net or sim")
	flag.Parse()
	if *api == "sim" {
		listen, listenPacket = sim.Listen, sim.ListenPacket
	}
	go echoPackets()

	control, err := listen("tcp", controlAddr)
	if err != nil {
		panic(err)
	}
	for {
		conn, err := control.Accept()
		if err != nil {
			panic(err)
		}
		go serveControl(conn)
	}
}

func portOf(l net.Listener) string {
	addr := l.Addr().String()
	return addr[strings.LastIndexByte(addr, ':')+1:]
}

func serveControl(conn net.Conn) {
	defer conn.Close()
	l, err := listen("tcp", ephemeralAny)
	if err != nil {
		return
	}
	defer l.Close()
	if _, err := fmt.Fprintln(conn, portOf(l)); err != nil {
		return
	}
	peer, err := l.Accept()
	if err != nil {
		return
	}
	defer peer.Close()
	request, err := io.ReadAll(peer)
	if err != nil {
		return
	}
	fmt.Fprintf(peer, "%s %s", peer.RemoteAddr(), strings.ToUpper(string(request)))
}

func echoPackets() {
	conn, err := listenPacket("udp", packetAddr)
	if err != nil {
		panic(err)
	}
	buf := make([]byte, maxDatagram)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			panic(err)
		}
		conn.WriteTo(buf[:n], from)
	}
}
