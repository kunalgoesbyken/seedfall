package sim

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestBuggifyIsInertOutsideTheSimulator(t *testing.T) {
	for range 100 {
		if Buggify("site") || BuggifyWithProb("site", 1) {
			t.Fatal("a buggify site fired outside the simulator")
		}
	}
}

func TestRandomChoiceCoversEveryItem(t *testing.T) {
	if msg := panics(func() { RandomChoice([]int(nil)) }); !strings.Contains(msg, "empty slice") {
		t.Fatalf("an empty choice gave %q", msg)
	}
	items := []string{"a", "b", "c"}
	seen := map[string]bool{}
	for range 300 {
		seen[RandomChoice(items)] = true
	}
	if len(seen) != len(items) {
		t.Fatalf("300 choices saw only %v", seen)
	}
}

func TestEventuallyOutsideTheSimulator(t *testing.T) {
	polls := 0
	if !Eventually(func() bool { polls++; return polls == 3 }, "third poll succeeds", time.Second) || polls != 3 {
		t.Fatalf("Eventually stopped after %d polls", polls)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an Eventually that is never true did not panic outside the simulator")
		}
	}()
	Eventually(func() bool { return false }, "never", 10*time.Millisecond)
}

func TestBudgetIsTheFullCountOutsideTheSimulator(t *testing.T) {
	for _, n := range []int{0, 1, 200} {
		if got := Budget(n); got != n {
			t.Fatalf("Budget(%d) = %d outside the simulator", n, got)
		}
	}
}

func panics(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

func TestNumericAndGroupedAssertionsOutsideTheSimulator(t *testing.T) {
	passing := []func(){
		func() { AlwaysGreaterThan(2, 1, "gt") },
		func() { AlwaysGreaterThanOrEqualTo(uint8(1), 1, "gte") },
		func() { AlwaysLessThan(1.5, 2, "lt") },
		func() { AlwaysLessThanOrEqualTo(int64(-3), -3, "lte") },
		func() { SometimesGreaterThan(0, 1, "sometimes never fails") },
		func() { AlwaysSome(map[string]bool{"a": false, "b": true}, "some") },
		func() { SometimesAll(map[string]bool{"a": false}, "all") },
		func() { Event("done", map[string]int{"n": 1}) },
	}
	for i, f := range passing {
		if msg := panics(f); msg != "" {
			t.Fatalf("case %d panicked: %s", i, msg)
		}
	}
	if msg := panics(func() { AlwaysGreaterThan(1, 2, "balance positive", "after crash") }); msg != `seedfall: balance positive: {"details":"after crash","left":1,"right":2}` {
		t.Fatalf("numeric failure %q", msg)
	}
	if msg := panics(func() { AlwaysSome(map[string]bool{"a": false, "b": false}, "one replica answers") }); msg != `seedfall: one replica answers: {"a":false,"b":false}` {
		t.Fatalf("grouped failure %q", msg)
	}
}

func TestNumericDetailsSurviveValuesJSONCannotEncode(t *testing.T) {
	if msg := panics(func() { AlwaysGreaterThan(math.NaN(), 0, "ratio is positive") }); !strings.Contains(msg, "left:NaN") {
		t.Fatalf("numeric failure %q", msg)
	}
}

func TestNetworkFunctionsRejectOtherNetworks(t *testing.T) {
	calls := map[string]func(network string) error{
		"Listen":       func(n string) error { _, err := Listen(n, "127.0.0.1:0"); return err },
		"ListenPacket": func(n string) error { _, err := ListenPacket(n, "127.0.0.1:0"); return err },
		"Dial":         func(n string) error { _, err := Dial(n, "127.0.0.1:1"); return err },
	}
	wrong := map[string][]string{"Listen": {"udp", "unix", ""}, "ListenPacket": {"tcp", "unixgram", ""}, "Dial": {"udp", "unix", "tcpx"}}
	for name, call := range calls {
		for _, network := range wrong[name] {
			if err := call(network); err == nil || !strings.Contains(err.Error(), "simulator supports only") {
				t.Errorf("%s(%q) returned %v", name, network, err)
			}
		}
	}
}

func TestListenAndDialAcceptTheIPNetworks(t *testing.T) {
	l, err := Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	p, err := ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
}
