package engine

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func sees(f Fault, names []string, a string) []string {
	var out []string
	for _, b := range names {
		if !f.affects(a, b) {
			out = append(out, b)
		}
	}
	return out
}

func TestPartitionTopologies(t *testing.T) {
	names := []string{"n1", "n2", "n3", "n4", "n5"}
	r := seeded(11)
	majority := len(names)/2 + 1
	for range 200 {
		if f := randomPartition(r, names, []string{TopologyOne}); len(f.Side) != 1 {
			t.Fatalf("one isolated %v, want a single node", f.Side)
		}
		if f := randomPartition(r, names, []string{TopologyMajority}); len(f.Side) != majority {
			t.Fatalf("majority side %v, want %d nodes", f.Side, majority)
		}
		ring := randomPartition(r, names, []string{TopologyRing})
		views := map[string]bool{}
		for _, n := range names {
			view := sees(ring, names, n)
			if len(view) < majority || len(view) == len(names) {
				t.Fatalf("ring %v: %s sees %v, want a majority but not everyone", ring.Groups, n, view)
			}
			views[strings.Join(view, ",")] = true
		}
		if len(views) != len(names) {
			t.Fatalf("ring %v: two nodes see the same majority", ring.Groups)
		}
		groups := randomPartition(r, names, []string{TopologyGroups}).Groups
		if len(groups) < 2 || len(slices.Concat(groups...)) != len(names) {
			t.Fatalf("random groups %v, want at least two disjoint groups covering every node", groups)
		}
	}
}

func TestDefaultTopologyKeepsTheHalfSchedule(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	a, b := seeded(4), seeded(4)
	for range 100 {
		if got, want := randomPartition(a, names, []string{TopologyHalf}).Side, randomSide(b, names); !slices.Equal(got, want) {
			t.Fatalf("half partition %v, want the randomSide draw %v", got, want)
		}
	}
}

func TestOneWayClogHoldsOneDirectionOnly(t *testing.T) {
	f := connected(t, Range{ms(2), ms(2)})
	lift := f.w.now + int64(time.Second)
	f.w.applyNetFault(Fault{Kind: FaultClog, From: "client", To: "server", Mode: ClogStop, Until: Duration(lift)})
	f.w.net.write(f.client, f.conn, []byte("held"))
	f.w.net.write(f.server, f.peer, []byte("free"))
	if got := f.w.net.endpoint(f.server, f.peer).inbox[0].at; got < lift {
		t.Fatalf("client -> server arrives at %s, want after the clog lifts at %s", time.Duration(got), time.Duration(lift))
	}
	if got := f.w.net.endpoint(f.client, f.conn).inbox[0].at; got != f.w.now+int64(ms(2)) {
		t.Fatalf("server -> client arrives at %s, want normal latency", time.Duration(got))
	}
	if got := f.w.net.dial(f.client, "server:7000"); got != codeRefused {
		t.Fatalf("dial over a stopped direction = %d, want codeRefused", got)
	}
}

func TestJamAcceptsLateAndSlowAddsDelay(t *testing.T) {
	f := connected(t, Range{ms(2), ms(2)})
	lift := f.w.now + int64(time.Second)
	f.w.applyNetFault(Fault{Kind: FaultClog, Node: "server", Dir: DirIn, Mode: ClogJam, Until: Duration(lift)})
	f.w.applyNetFault(Fault{Kind: FaultClog, Node: "client", Dir: DirIn, Mode: ClogSlow, Delay: ms(50), Until: Duration(lift)})
	if got := f.w.net.dial(f.client, "server:7000"); got < 0 {
		t.Fatalf("dial into a jammed node = %d, want it to hang rather than fail", got)
	}
	if f.server.wakeAt < lift {
		t.Fatalf("jammed connection is accepted at %s, want after %s", time.Duration(f.server.wakeAt), time.Duration(lift))
	}
	f.w.net.write(f.server, f.peer, []byte("slow"))
	if got, want := f.w.net.endpoint(f.client, f.conn).inbox[0].at, f.w.now+int64(ms(52)); got != want {
		t.Fatalf("slowed message arrives at %s, want %s", time.Duration(got), time.Duration(want))
	}
	f.w.now = lift
	f.w.net.write(f.client, f.conn, []byte("after"))
	if got := f.w.net.endpoint(f.server, f.peer).inbox[0].at; got != lift+int64(ms(2)) {
		t.Fatalf("message after the clog lifts arrives at %s, want normal latency", time.Duration(got))
	}
}

func TestLinkLatencyOverridesOneDirection(t *testing.T) {
	f := connected(t, Range{ms(1), ms(1)})
	f.w.cfg.Links = []Link{{From: "server", Latency: Range{ms(40), ms(40)}}}
	f.w.net.write(f.server, f.peer, []byte("x"))
	f.w.net.write(f.client, f.conn, []byte("y"))
	if got := f.w.net.endpoint(f.client, f.conn).inbox[0].at; got != f.w.now+int64(ms(40)) {
		t.Fatalf("server -> client arrives at %s, want the 40ms link latency", time.Duration(got))
	}
	if got := f.w.net.endpoint(f.server, f.peer).inbox[0].at; got != f.w.now+int64(ms(1)) {
		t.Fatalf("client -> server arrives at %s, want the default latency", time.Duration(got))
	}
}

func TestResetBreaksConnectionsWithoutClosingListeners(t *testing.T) {
	f := connected(t, Range{ms(3), ms(3)})
	f.w.net.write(f.client, f.conn, []byte("lost"))
	if got := f.w.net.reset(f.server); got != 1 {
		t.Fatalf("reset %d connections, want 1", got)
	}
	buf := make([]byte, 8)
	if got := f.w.net.read(f.server, f.peer, buf); got != codeReset {
		t.Fatalf("read on the reset node = %d, want codeReset at once", got)
	}
	if got := f.w.net.write(f.server, f.peer, buf); got != codeReset {
		t.Fatalf("write on the reset node = %d, want codeReset", got)
	}
	if got := f.w.net.write(f.client, f.conn, buf); got != codeReset {
		t.Fatalf("peer write after the reset = %d, want codeReset", got)
	}
	f.w.now += int64(ms(3))
	if got := f.w.net.read(f.client, f.conn, buf); got != codeReset {
		t.Fatalf("peer read after the reset = %d, want codeReset", got)
	}
	if got := f.w.net.dial(f.client, "server:7000"); got < 0 {
		t.Fatalf("dial after a reset = %d, want the listener to stay open", got)
	}
}

func TestResetBreaksConnectionsWaitingToBeAccepted(t *testing.T) {
	f := connected(t, Range{ms(3), ms(3)})
	waiting := f.w.net.dial(f.client, "server:7000")
	f.w.now += int64(ms(3))
	inFlight := f.w.net.dial(f.client, "server:7000")
	if got := f.w.net.reset(f.server); got != 2 {
		t.Fatalf("reset %d connections, want the open one and the one waiting in the backlog", got)
	}
	buf := make([]byte, 8)
	if got := f.w.net.write(f.client, waiting, buf); got != codeReset {
		t.Fatalf("write on a reset backlog connection = %d, want codeReset", got)
	}
	if got := f.w.net.write(f.client, inFlight, buf); got < 0 {
		t.Fatalf("write on a connection still in flight = %d, want it to survive the reset", got)
	}
}

func TestSlowNodeStretchesItsLatency(t *testing.T) {
	f := connected(t, Range{ms(4), ms(4)})
	f.w.nodes = []*node{f.server.node, f.client.node}
	f.server.node.slowFactor, f.server.node.slowUntil = 3, f.w.now+int64(time.Second)
	f.w.net.write(f.client, f.conn, []byte("x"))
	if got := f.w.net.endpoint(f.server, f.peer).inbox[0].at; got != f.w.now+int64(ms(12)) {
		t.Fatalf("message to a 3x slow node arrives at %s, want 12ms", time.Duration(got))
	}
	if got := f.w.stretch(int64(time.Second), f.server.node); got != int64(3*time.Second) {
		t.Fatalf("a 1s timer on a 3x slow node takes %s, want 3s", time.Duration(got))
	}
	f.w.healNetwork()
	if got := f.w.stretch(int64(time.Second), f.server.node); got != int64(time.Second) {
		t.Fatalf("after healing a 1s timer takes %s, want 1s", time.Duration(got))
	}
}

func TestQuietHealsClogs(t *testing.T) {
	f := connected(t, Range{ms(1), ms(1)})
	f.w.applyNetFault(Fault{Kind: FaultClog, Node: "server", Dir: DirBoth, Mode: ClogStop, Until: Duration(f.w.now + int64(time.Hour))})
	f.w.healNetwork()
	if f.w.cut("client", "server") {
		t.Fatal("a healed clog still cuts the link")
	}
}

func TestGenerateNetworkFaults(t *testing.T) {
	cfg := faultConfig()
	cfg.Faults.PartitionTopologies = topologies
	cfg.Faults.ClogPerSec, cfg.Faults.SwizzlePerSec, cfg.Faults.ResetPerSec, cfg.Faults.SlowPerSec = 0.5, 0.2, 0.3, 0.3
	if err := cfg.validateNetwork(map[string]bool{"n1": true, "n2": true, "n3": true, "client": true}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for seed := range uint64(30) {
		for _, f := range faultsFor(cfg, seed) {
			seen[f.Kind+f.Mode] = true
			switch f.Kind {
			case FaultSlow:
				if f.Factor < 1 || f.Factor > cfg.Faults.SlowFactor {
					t.Fatalf("seed %d: slow factor %v outside [1, %v]", seed, f.Factor, cfg.Faults.SlowFactor)
				}
			case FaultClog:
				if f.Until <= f.At || (f.Dir == "" && (f.From == f.To || f.From == "")) {
					t.Fatalf("seed %d: malformed clog %+v", seed, f)
				}
				if (f.Mode == ClogSlow) != (f.Delay > 0) {
					t.Fatalf("seed %d: clog %+v has a delay that does not match its mode", seed, f)
				}
			}
			if !strings.Contains(f.Describe(), f.Node) {
				t.Fatalf("seed %d: %q does not name node %q", seed, f.Describe(), f.Node)
			}
		}
	}
	for _, k := range []string{FaultClog + ClogStop, FaultClog + ClogSlow, FaultClog + ClogJam, FaultReset, FaultSlow, FaultPartition} {
		if !seen[k] {
			t.Errorf("no seed scheduled %s", k)
		}
	}
}

func TestSwizzleClogsOneAtATimeThenUnclogsInRandomOrder(t *testing.T) {
	spec := defaultConfig().Faults
	names := []string{"a", "b", "c", "d", "e", "f"}
	r := seeded(8)
	orders := map[string]bool{}
	for range 50 {
		clogs, end := swizzle(r, time.Second, names, spec)
		starts := map[Duration]bool{}
		var lifted []string
		byUntil := slices.Clone(clogs)
		slices.SortFunc(byUntil, func(a, b Fault) int { return int(a.Until - b.Until) })
		for _, c := range byUntil {
			if c.Until <= c.At || time.Duration(c.Until) > end || starts[c.At] {
				t.Fatalf("swizzle %+v: clogs must start one at a time and lift before %s", clogs, end)
			}
			starts[c.At] = true
			lifted = append(lifted, c.Node)
		}
		orders[strings.Join(lifted, "")] = true
	}
	if len(orders) < 10 {
		t.Fatalf("only %d distinct unclog orders over 50 swizzles", len(orders))
	}
}

func TestValidateNetwork(t *testing.T) {
	small := map[string]bool{"n1": true, "client": true}
	alone := map[string]bool{"client": true}
	for name, c := range map[string]struct {
		names  map[string]bool
		mutate func(*Config)
	}{
		"unknown link node":     {small, func(c *Config) { c.Links = []Link{{From: "nope"}} }},
		"unknown topology":      {small, func(c *Config) { c.Faults.PartitionTopologies = []string{"star"} }},
		"unknown clog mode":     {small, func(c *Config) { c.Faults.ClogModes = []string{"drop"} }},
		"ring too small":        {small, func(c *Config) { c.Faults.PartitionPerSec, c.Faults.PartitionTopologies = 1, []string{TopologyRing} }},
		"no clog modes":         {small, func(c *Config) { c.Faults.ClogPerSec, c.Faults.ClogModes = 1, nil }},
		"slow factor of 1":      {small, func(c *Config) { c.Faults.SlowPerSec, c.Faults.SlowFactor = 1, 1 }},
		"partition on one node": {alone, func(c *Config) { c.Faults.PartitionPerSec = 1 }},
		"clog on one node":      {alone, func(c *Config) { c.Faults.ClogPerSec = 1 }},
		"swizzle on one node":   {alone, func(c *Config) { c.Faults.SwizzlePerSec = 1 }},
	} {
		cfg := defaultConfig()
		c.mutate(&cfg)
		if cfg.validateNetwork(c.names) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	cfg := defaultConfig()
	if err := cfg.validateNetwork(small); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
}

func TestSlowClogAddsItsDelayToHeldTraffic(t *testing.T) {
	f := connected(t, Range{ms(2), ms(2)})
	heal := f.w.now + int64(time.Second)
	f.w.applyNetFault(Fault{Kind: FaultPartition, Side: []string{"server"}, Until: Duration(heal)})
	f.w.applyNetFault(Fault{Kind: FaultClog, From: "client", To: "server", Mode: ClogSlow, Delay: ms(30), Until: Duration(heal)})
	f.w.net.write(f.client, f.conn, []byte("x"))
	if got, want := f.w.net.endpoint(f.server, f.peer).inbox[0].at, heal+int64(ms(32)); got != want {
		t.Fatalf("held and slowed message arrives at %s, want %s", time.Duration(got), time.Duration(want))
	}
}
