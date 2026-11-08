package engine

import (
	"math"
	"testing"
	"time"
)

type udpFixture struct {
	w    *world
	a, b *instance
	sa   int32
	sb   int32
}

func udpPair(t *testing.T, latency Range) *udpFixture {
	t.Helper()
	cfg := &Config{Nodes: []NodeSpec{{Name: "a"}, {Name: "b"}}, Workload: NodeSpec{Name: "w"}, Latency: latency}
	w := &world{cfg: cfg, netRand: streams(1, "net"), udpRand: streams(1, "udp"), trace: &tracer{}}
	w.net = newNetwork(w)
	inst := func(name string) *instance {
		return &instance{node: &node{spec: NodeSpec{Name: name}}, label: name + "#1", wakeAt: math.MaxInt64}
	}
	f := &udpFixture{w: w, a: inst("a"), b: inst("b")}
	if f.sa = w.net.bindUDP(f.a, ":9000"); f.sa < 0 {
		t.Fatalf("bind a: %d", f.sa)
	}
	if f.sb = w.net.bindUDP(f.b, ":9000"); f.sb < 0 {
		t.Fatalf("bind b: %d", f.sb)
	}
	return f
}

func (f *udpFixture) recv(t *testing.T) (string, string) {
	t.Helper()
	buf := make([]byte, 64)
	n, from := f.w.net.recvfrom(f.b, f.sb, buf)
	if n < 0 {
		t.Fatalf("recvfrom: %d", n)
	}
	return string(buf[:n]), from
}

func TestBindPortZeroAndDuplicateBindFails(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	if got := f.w.net.bindUDP(f.a, ":9000"); got != codeInUse {
		t.Fatalf("second bind = %d, want codeInUse", got)
	}
	id := f.w.net.bindUDP(f.a, ":0")
	if addr, _ := f.w.net.addr(f.a, id, false); addr == "a:0" {
		t.Fatalf("port 0 bound to %s", addr)
	}
	if got := f.w.net.bindUDP(f.a, "b:9001"); got != codeRefused {
		t.Fatalf("bind on another node = %d, want codeRefused", got)
	}
}

func TestDatagramsArriveAfterLatencyWithSender(t *testing.T) {
	f := udpPair(t, Range{ms(5), ms(5)})
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("hi"))
	if n, _ := f.w.net.recvfrom(f.b, f.sb, make([]byte, 8)); n != codeAgain {
		t.Fatalf("recv before arrival = %d, want codeAgain", n)
	}
	if f.b.wakeAt != int64(ms(5)) {
		t.Fatalf("receiver wakes at %d, want %d", f.b.wakeAt, int64(ms(5)))
	}
	f.w.now = int64(ms(5))
	if data, from := f.recv(t); data != "hi" || from != "a:9000" {
		t.Fatalf("got %q from %q", data, from)
	}
}

func TestDatagramsReorderUnderRandomLatency(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(50)})
	for i := range 100 {
		f.w.net.sendto(f.a, f.sa, "b:9000", []byte{byte(i)})
	}
	inbox := f.w.net.udp["b:9000"].inbox
	out := false
	for i := 1; i < len(inbox); i++ {
		if inbox[i].at < inbox[i-1].at {
			t.Fatalf("inbox not sorted by arrival at %d", i)
		}
		out = out || inbox[i].data[0] < inbox[i-1].data[0]
	}
	if !out {
		t.Fatal("no datagram overtook another under 1ms..50ms latency")
	}
}

func TestDatagramIsTruncatedToTheReadBuffer(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("long datagram"))
	f.w.now = int64(ms(1))
	buf := make([]byte, 4)
	if n, _ := f.w.net.recvfrom(f.b, f.sb, buf); n != 4 || string(buf) != "long" {
		t.Fatalf("read %d %q", n, buf)
	}
	if n, _ := f.w.net.recvfrom(f.b, f.sb, buf); n != codeAgain {
		t.Fatalf("the rest of a datagram must be discarded, got %d", n)
	}
}

func TestPartitionAndStopClogDropDatagramsButSlowClogDelays(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	until := int64(time.Second)
	f.w.applyNetFault(Fault{Kind: FaultPartition, Side: []string{"a"}, Until: Duration(until)})
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("lost"))
	if got := len(f.w.net.udp["b:9000"].inbox); got != 0 {
		t.Fatalf("%d datagrams crossed a partition", got)
	}
	f.w.netFaults = nil
	f.w.applyNetFault(Fault{Kind: FaultClog, From: "a", To: "b", Mode: ClogStop, Until: Duration(until)})
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("lost"))
	f.w.net.sendto(f.b, f.sb, "a:9000", []byte("back"))
	if got := len(f.w.net.udp["b:9000"].inbox); got != 0 {
		t.Fatalf("%d datagrams crossed a stopped link", got)
	}
	if got := len(f.w.net.udp["a:9000"].inbox); got != 1 {
		t.Fatalf("the reverse direction delivered %d datagrams, want 1", got)
	}
	f.w.netFaults = nil
	f.w.applyNetFault(Fault{Kind: FaultClog, From: "a", To: "b", Mode: ClogJam, Until: Duration(until)})
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("jammed"))
	if got := len(f.w.net.udp["b:9000"].inbox); got != 0 {
		t.Fatalf("%d datagrams crossed a jammed link", got)
	}
	f.w.netFaults = nil
	f.w.applyNetFault(Fault{Kind: FaultClog, From: "a", To: "b", Mode: ClogSlow, Delay: ms(100), Until: Duration(until)})
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("slow"))
	if at := f.w.net.udp["b:9000"].inbox[0].at; at != int64(ms(101)) {
		t.Fatalf("slow datagram arrives at %d, want %d", at, int64(ms(101)))
	}
}

func TestDatagramToAnUnboundPortIsLost(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	if got := f.w.net.sendto(f.a, f.sa, "b:9999", []byte("x")); got != 1 {
		t.Fatalf("sendto = %d, want 1", got)
	}
	if got := f.w.net.sendto(f.a, f.sa, "", []byte("x")); got != codeNoDest {
		t.Fatalf("sendto without a destination = %d, want codeNoDest", got)
	}
}

func TestConnectedSocketOnlyHearsItsPeer(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.net.connectUDP(f.b, f.sb, "a:1234")
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("x"))
	if got := len(f.w.net.udp["b:9000"].inbox); got != 0 {
		t.Fatalf("connected socket received %d datagrams from another address", got)
	}
}

func TestUDPDropAndDupRatesAndExplicitReplay(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.spec.UDPLoss, f.w.spec.UDPDup = 0.3, 0.3
	const sent = 2000
	for range sent {
		f.w.net.sendto(f.a, f.sa, "b:9000", []byte("x"))
	}
	drops, dups := 0, 0
	for _, g := range f.w.ioFaults {
		switch g.Kind {
		case FaultUDPDrop:
			drops++
		case FaultUDPDup:
			dups++
		}
		if g.Node != "a" || g.To != "b" || g.Op < 1 {
			t.Fatalf("fault %+v", g)
		}
	}
	if drops < sent/5 || drops > 2*sent/5 || dups < sent/8 {
		t.Fatalf("drops %d dups %d of %d", drops, dups, sent)
	}
	want := sent - drops + dups
	if got := len(f.w.net.udp["b:9000"].inbox); got != want {
		t.Fatalf("inbox holds %d, want %d", got, want)
	}
	replay := udpPair(t, Range{ms(1), ms(1)})
	replay.w.explicit, replay.w.faults = true, f.w.ioFaults
	for range sent {
		replay.w.net.sendto(replay.a, replay.sa, "b:9000", []byte("x"))
	}
	if got := len(replay.w.net.udp["b:9000"].inbox); got != want {
		t.Fatalf("replay delivered %d, want %d", got, want)
	}
}

func TestShieldedNodesTakeNoRandomUDPLoss(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.spec.UDPLoss = 1
	f.w.shielded = map[string]bool{"b": true}
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("x"))
	if got := len(f.w.net.udp["b:9000"].inbox); got != 1 {
		t.Fatalf("shielded node received %d datagrams, want 1", got)
	}
}

func TestCrashClosesTheNodesUDPSockets(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.net.drop(f.b)
	if _, ok := f.w.net.udp["b:9000"]; ok {
		t.Fatal("socket survived the crash")
	}
	if got := f.w.net.bindUDP(f.b, ":9000"); got < 0 {
		t.Fatalf("rebind after crash: %d", got)
	}
}

func TestConnectedSenderIsRefusedOnceAfterARoundTrip(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.net.connectUDP(f.a, f.sa, "b:9999")
	f.w.net.sendto(f.a, f.sa, "", []byte("x"))
	if n, _ := f.w.net.recvfrom(f.a, f.sa, make([]byte, 8)); n != codeAgain || f.w.net.readable(f.sa) {
		t.Fatalf("refused before the round trip: recv = %d", n)
	}
	if f.a.wakeAt != int64(ms(2)) || f.w.net.nextArrival(f.sa) != int64(ms(2)) {
		t.Fatalf("sender wakes at %d and next arrival is %d, want %d", f.a.wakeAt, f.w.net.nextArrival(f.sa), int64(ms(2)))
	}
	f.w.now = int64(ms(2))
	if !f.w.net.readable(f.sa) {
		t.Fatal("a refusal should make the socket readable")
	}
	if n, _ := f.w.net.recvfrom(f.a, f.sa, make([]byte, 8)); n != codeRefused {
		t.Fatalf("recv = %d, want codeRefused", n)
	}
	if n, _ := f.w.net.recvfrom(f.a, f.sa, make([]byte, 8)); n != codeAgain {
		t.Fatalf("second recv = %d, want the refusal reported once", n)
	}
	f.w.net.sendto(f.a, f.sa, "", []byte("x"))
	f.w.now += int64(ms(2))
	if got := f.w.net.sendto(f.a, f.sa, "", []byte("x")); got != codeRefused {
		t.Fatalf("sendto after a refusal = %d, want codeRefused", got)
	}
}

func TestPortUnreachableNeedsAConnectedSenderAndAPathBack(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.net.sendto(f.a, f.sa, "b:9999", []byte("x"))
	f.w.now = int64(ms(10))
	if f.w.net.readable(f.sa) {
		t.Fatal("an unconnected sender heard about an unbound port")
	}
	f.w.net.connectUDP(f.a, f.sa, "b:9999")
	f.w.applyNetFault(Fault{Kind: FaultClog, From: "b", To: "a", Mode: ClogStop, Until: Duration(time.Hour)})
	f.w.net.sendto(f.a, f.sa, "", []byte("x"))
	f.w.now += int64(ms(10))
	if f.w.net.readable(f.sa) {
		t.Fatal("a refusal crossed a stopped return path")
	}
}

func TestSenderKeepsItsSocketIDAcrossTheDatagramsLife(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("x"))
	flight := f.w.net.inflight()
	if len(flight) != 1 || flight[0].Conn != f.sa {
		t.Fatalf("in flight %+v, want the sender's socket %d", flight, f.sa)
	}
}

func TestDatagramLostToAConnectedSocketSaysSo(t *testing.T) {
	f := udpPair(t, Range{ms(1), ms(1)})
	f.w.rec = &recorder{}
	f.w.net.connectUDP(f.b, f.sb, "a:1234")
	f.w.net.sendto(f.a, f.sa, "b:9000", []byte("x"))
	if got := f.w.rec.messages; len(got) != 1 || got[0].Lost != lostConnected {
		t.Fatalf("messages %+v, want one lost with %q", got, lostConnected)
	}
}

func TestBranchRerandomizesUDPLoss(t *testing.T) {
	w, _ := buggifyWorld(13)
	w.branch, w.udpRand = &branch{key: 1}, streams(13, "udp")
	if !w.takeBranch(0) {
		t.Fatal("branch not taken")
	}
	if got, want := w.udpRand.Uint64(), streams(w.streamSeed(), "udp").Uint64(); got != want {
		t.Fatalf("udp stream after a branch drew %d, want the branch's %d", got, want)
	}
}
