package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const (
	rounds      = 20
	retry       = 100 * time.Millisecond
	readTimeout = 200 * time.Millisecond
	maxDatagram = 1500
	unboundAddr = "server:7199"
)

var (
	dial         = net.Dial
	listenPacket = net.ListenPacket
)

type halfCloser interface {
	CloseRead() error
	CloseWrite() error
}

func main() {
	api := flag.String("api", "net", "net or sim")
	flag.Parse()
	if *api == "sim" {
		dial, listenPacket = sim.Dial, sim.ListenPacket
	}
	for round := range rounds {
		exchangeStream(round)
		exchangePackets(round)
		if *api == "net" {
			refusedOnUnboundPort()
		}
	}
	fmt.Println("every half-close and datagram exchange succeeded")
}

func exchangeStream(round int) {
	for {
		if streamOnce(round) {
			return
		}
		time.Sleep(retry)
	}
}

func streamOnce(round int) bool {
	control, err := dial("tcp", "server:7000")
	if err != nil {
		return false
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(control).ReadString('\n')
	if err != nil {
		return false
	}
	sim.Always(control.(halfCloser).CloseRead() == nil, "CloseRead succeeds on a live connection")
	_, err = control.Read(make([]byte, 1))
	sim.Always(err == io.EOF, "reading after CloseRead returns EOF", "got ", err)
	conn, err := dial("tcp", "server:"+strings.TrimSpace(line))
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	request := fmt.Sprintf("round %d", round)
	if _, err := io.WriteString(conn, request); err != nil {
		return false
	}
	sim.Always(conn.(halfCloser).CloseWrite() == nil, "CloseWrite succeeds on a live connection")
	reply, err := io.ReadAll(conn)
	if err != nil || len(reply) == 0 {
		return false
	}
	want := fmt.Sprintf("%s %s", conn.LocalAddr(), strings.ToUpper(request))
	sim.Always(string(reply) == want, "the server sees the dialer's real address and reads to EOF after CloseWrite", "got ", string(reply), " want ", want)
	_, err = conn.Write([]byte("late"))
	sim.Always(errors.Is(err, syscall.EPIPE), "writing after CloseWrite fails with EPIPE", "got ", err)
	return true
}

func exchangePackets(round int) {
	conn, err := listenPacket("udp", ":0")
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	sim.Always(!strings.HasSuffix(conn.LocalAddr().String(), ":0"), "port 0 binds a real port", "bound ", conn.LocalAddr().String())
	server := resolve("server:7100")
	request := fmt.Sprintf("packet %d", round)
	buf := make([]byte, maxDatagram)
	for {
		conn.WriteTo([]byte(request), server)
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}
		if string(buf[:n]) != request {
			continue
		}
		sim.Always(portOf(from.String()) == "7100", "a reply carries the server's address", "from ", from.String())
		return
	}
}

func refusedOnUnboundPort() {
	conn, err := net.Dial("udp", unboundAddr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	buf := make([]byte, maxDatagram)
	for {
		conn.SetDeadline(time.Now().Add(readTimeout))
		if _, err = conn.Write([]byte("anyone there")); err == nil {
			_, err = conn.Read(buf)
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return
		}
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			sim.Fail(fmt.Sprint("a connected socket to an unbound port got ", err))
		}
	}
}

func portOf(addr string) string { return addr[strings.LastIndexByte(addr, ':')+1:] }

func resolve(addr string) net.Addr {
	if a, err := net.ResolveUDPAddr("udp", addr); err == nil {
		return a
	}
	return stringAddr(addr)
}

type stringAddr string

func (a stringAddr) Network() string { return "udp" }
func (a stringAddr) String() string  { return string(a) }
