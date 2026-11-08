package engine

import (
	"math"
	"testing"
	"time"
)

type netFixture struct {
	w              *world
	client, server *instance
	conn, peer     int32
}

func connected(t *testing.T, latency Range) *netFixture {
	t.Helper()
	cfg := &Config{Nodes: []NodeSpec{{Name: "server"}}, Workload: NodeSpec{Name: "client"}, Latency: latency}
	w := &world{cfg: cfg, netRand: streams(1, "net")}
	w.net = newNetwork(w)
	inst := func(name string) *instance {
		return &instance{node: &node{spec: NodeSpec{Name: name}}, wakeAt: math.MaxInt64}
	}
	f := &netFixture{w: w, client: inst("client"), server: inst("server")}
	l := w.net.listen(f.server, ":7000")
	if l < 0 {
		t.Fatalf("listen: %d", l)
	}
	if f.conn = w.net.dial(f.client, "server:7000"); f.conn < 0 {
		t.Fatalf("dial: %d", f.conn)
	}
	if got := w.net.accept(f.server, l); got != codeAgain {
		t.Fatalf("accept before the connection arrived = %d, want codeAgain", got)
	}
	w.now = f.server.wakeAt
	if f.peer = w.net.accept(f.server, l); f.peer < 0 {
		t.Fatalf("accept: %d", f.peer)
	}
	f.server.wakeAt = math.MaxInt64
	return f
}

func ms(n int) Duration { return Duration(time.Duration(n) * time.Millisecond) }

func TestSendPreservesOrderUnderRandomLatency(t *testing.T) {
	f := connected(t, Range{ms(1), ms(50)})
	start := f.w.now
	for i := range 100 {
		f.w.net.write(f.client, f.conn, []byte{byte(i)})
	}
	inbox := f.w.net.endpoint(f.server, f.peer).inbox
	for i, c := range inbox {
		if c.at < start+int64(ms(1)) {
			t.Fatalf("chunk %d arrives at %d, sooner than the minimum latency", i, c.at)
		}
		if i > 0 && c.at < inbox[i-1].at {
			t.Fatalf("chunk %d arrives at %d, before chunk %d at %d", i, c.at, i-1, inbox[i-1].at)
		}
	}
	if f.server.wakeAt != inbox[0].at {
		t.Fatalf("receiver wakes at %d, want the first arrival %d", f.server.wakeAt, inbox[0].at)
	}
	f.w.now = inbox[len(inbox)-1].at
	buf := make([]byte, 1)
	for i := range 100 {
		if n := f.w.net.read(f.server, f.peer, buf); n != 1 || buf[0] != byte(i) {
			t.Fatalf("read %d returned %d bytes %v, want byte %d", i, n, buf, i)
		}
	}
}

func TestReadWaitsForArrival(t *testing.T) {
	f := connected(t, Range{ms(5), ms(5)})
	f.w.net.write(f.client, f.conn, []byte("hello"))
	buf := make([]byte, 16)
	if got := f.w.net.read(f.server, f.peer, buf); got != codeAgain {
		t.Fatalf("read before arrival = %d, want codeAgain", got)
	}
	f.w.now += int64(ms(5))
	if got := f.w.net.read(f.server, f.peer, buf); string(buf[:max(got, 0)]) != "hello" {
		t.Fatalf("read after arrival = %d %q", got, buf[:max(got, 0)])
	}
}

func TestPartitionHoldsMessagesUntilHeal(t *testing.T) {
	f := connected(t, Range{ms(2), ms(4)})
	heal := f.w.now + int64(time.Second)
	f.w.applyNetFault(Fault{Kind: FaultPartition, Side: []string{"server"}, Until: Duration(heal)})
	if !f.w.cut("client", "server") {
		t.Fatal("client and server should be cut")
	}
	if f.w.cut("client", "client") {
		t.Fatal("a node is never cut from itself")
	}
	f.w.net.write(f.client, f.conn, []byte("held"))
	held := f.w.net.endpoint(f.server, f.peer).inbox[0].at
	if held < heal+int64(ms(2)) || held > heal+int64(ms(4)) {
		t.Fatalf("partitioned message arrives at %s, want within latency after heal at %s", time.Duration(held), time.Duration(heal))
	}
	if got := f.w.net.dial(f.client, "server:7000"); got != codeRefused {
		t.Fatalf("dial across a partition = %d, want codeRefused", got)
	}
	f.w.now = heal
	f.w.net.write(f.server, f.peer, []byte("after"))
	back := f.w.net.endpoint(f.client, f.conn).inbox[0].at
	if back > heal+int64(ms(4)) {
		t.Fatalf("message after heal arrives at %s, want within normal latency", time.Duration(back))
	}
}

func TestCloseDeliversEOFAfterData(t *testing.T) {
	f := connected(t, Range{ms(1), ms(9)})
	f.w.net.write(f.client, f.conn, []byte("bye"))
	f.w.net.close(f.client, f.conn)
	f.w.now += int64(ms(20))
	buf := make([]byte, 8)
	if got := f.w.net.read(f.server, f.peer, buf); string(buf[:max(got, 0)]) != "bye" {
		t.Fatalf("read = %d, want the data before EOF", got)
	}
	if got := f.w.net.read(f.server, f.peer, buf); got != codeEOF {
		t.Fatalf("read after data = %d, want codeEOF", got)
	}
	if got := f.w.net.write(f.server, f.peer, []byte("x")); got != codeReset {
		t.Fatalf("write to a closed peer = %d, want codeReset", got)
	}
}

func TestDropResetsEveryConnectionOfACrashedInstance(t *testing.T) {
	f := connected(t, Range{ms(1), ms(1)})
	f.w.net.drop(f.server)
	f.w.now += int64(ms(1))
	if got := f.w.net.read(f.client, f.conn, make([]byte, 1)); got != codeReset {
		t.Fatalf("read from a crashed peer = %d, want codeReset", got)
	}
	if got := f.w.net.dial(f.client, "server:7000"); got != codeRefused {
		t.Fatalf("dial a crashed listener = %d, want codeRefused", got)
	}
}
