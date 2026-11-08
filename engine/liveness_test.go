package engine

import (
	"context"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func livenessRunner(t *testing.T, edit func(cfg *Config, wasm string)) *Runner {
	t.Helper()
	wasm := guestWasm(t)
	cfg := defaultConfig()
	cfg.Nodes = []NodeSpec{{Name: "store", Wasm: wasm, Args: []string{"-role", "store"}}}
	cfg.Workload = NodeSpec{Name: "client", Wasm: wasm, Args: []string{"-role", "client", "-writes", "20"}}
	cfg.Duration = Duration(2 * time.Second)
	cfg.Dir = filepath.Dir(wasm)
	edit(&cfg, wasm)
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.rt.Close(context.Background()) })
	return r
}

func traced(res Result, text string) int {
	return slices.IndexFunc(res.Trace, func(l string) bool { return strings.Contains(l, text) })
}

func TestLivenessFailsAWorkloadThatNeverFinishes(t *testing.T) {
	forever := func(cfg *Config, wasm string) {
		cfg.Workload.Args = append(cfg.Workload.Args, "-forever")
	}
	if res := livenessRunner(t, forever).Run(1, nil); res.Failed || res.Finished {
		t.Fatalf("without liveness a workload that never finishes passed at the horizon, got failed=%v finished=%v: %s", res.Failed, res.Finished, res.Reason)
	}
	res := livenessRunner(t, func(cfg *Config, wasm string) {
		forever(cfg, wasm)
		cfg.Liveness.Within = Duration(time.Second)
	}).Run(1, nil)
	if !res.Failed || res.Failure != (Failure{Class: FailLiveness, Node: "client"}) {
		t.Fatalf("got failure %+v (%s), want a liveness failure of client", res.Failure, res.Reason)
	}
	if res.At != 3*time.Second || !strings.Contains(res.Reason, "did not finish within 1s of the recovery phase starting at 2.000s") {
		t.Fatalf("failed at %s with %q, want 3s and the deadline in the reason", res.At, res.Reason)
	}
	if traced(res, "recovery: the fault period ended") < 0 {
		t.Fatal("the trace does not record the recovery phase")
	}
}

func TestRecoveryRestartsCrashedNodesAndHealsTheNetwork(t *testing.T) {
	broken := func(cfg *Config, wasm string) {
		cfg.Duration = Duration(time.Second)
		cfg.Faults.CrashPerSec = 5
		cfg.Faults.RestartAfter = Range{Duration(time.Minute), Duration(time.Minute)}
		cfg.Faults.PartitionPerSec = 5
		cfg.Faults.PartitionFor = Range{Duration(time.Minute), Duration(time.Minute)}
	}
	for seed := range uint64(3) {
		if res := livenessRunner(t, broken).Run(seed, nil); res.Finished {
			t.Fatalf("seed %d finished although the store stays down past the horizon", seed)
		}
		res := livenessRunner(t, func(cfg *Config, wasm string) {
			broken(cfg, wasm)
			cfg.Liveness.Within = Duration(5 * time.Second)
		}).Run(seed, nil)
		if res.Failed || !res.Finished {
			t.Fatalf("seed %d: failed=%v finished=%v %s, want the recovery phase to let the workload finish", seed, res.Failed, res.Finished, res.Reason)
		}
		if traced(res, "1 crashed nodes restart now") < 0 {
			t.Fatalf("seed %d: the recovery phase did not restart the crashed store", seed)
		}
	}
}

func TestFinallyRunsAfterTheWorkloadWithFaultsStopped(t *testing.T) {
	r := livenessRunner(t, func(cfg *Config, wasm string) {
		cfg.Duration = Duration(30 * time.Second)
		cfg.Finally = &NodeSpec{Name: "check", Wasm: wasm, Args: []string{"-role", "finally", "-least", "1"}}
		cfg.Faults.CrashPerSec = 1
		cfg.Faults.RestartAfter = Range{Duration(10 * time.Millisecond), Duration(100 * time.Millisecond)}
	})
	for seed := range uint64(3) {
		res := r.Run(seed, nil)
		if res.Failed || !res.Finished {
			t.Fatalf("seed %d: failed=%v finished=%v %s", seed, res.Failed, res.Finished, res.Reason)
		}
		done, start, passed := traced(res, "workload finished, the finally check runs next"), traced(res, "start check#1"), traced(res, "finally check check#1 passed")
		if done < 0 || start < done || passed < start {
			t.Fatalf("seed %d: trace order workload done %d, finally start %d, finally passed %d", seed, done, start, passed)
		}
		if slices.ContainsFunc(res.Trace[done:], func(l string) bool { return strings.Contains(l, "fault crash") }) {
			t.Fatalf("seed %d: a crash fault fired after the workload finished", seed)
		}
		if p := res.Properties["the store answers after recovery"]; p == nil || p.Kind != Eventually || p.True != 1 {
			t.Fatalf("seed %d: eventually property %+v, want one true observation", seed, p)
		}
		if slices.ContainsFunc(res.Faults, func(f Fault) bool { return f.Node == "check" }) {
			t.Fatalf("seed %d: the finally check was scheduled for a fault", seed)
		}
	}
}

func TestEventuallyNeverTrueIsALivenessFailure(t *testing.T) {
	res := livenessRunner(t, func(cfg *Config, wasm string) {
		cfg.Finally = &NodeSpec{Name: "check", Wasm: wasm, Args: []string{"-role", "finally", "-least", "1000000", "-within", "500ms"}}
	}).Run(1, nil)
	want := Failure{Class: FailLiveness, Node: "check", Property: "the store answers after recovery"}
	if !res.Failed || res.Failure != want || !strings.Contains(res.Reason, "not true within 500ms") {
		t.Fatalf("got %+v %q, want %+v", res.Failure, res.Reason, want)
	}
}

func TestWorkloadFaultsCrashTheClientAndReplayExactly(t *testing.T) {
	crashes := func(include bool) func(*Config, string) {
		return func(cfg *Config, wasm string) {
			cfg.Duration = Duration(20 * time.Second)
			cfg.Workload.Args = append(cfg.Workload.Args, "-history")
			cfg.Faults.CrashPerSec = 2
			cfg.Faults.RestartAfter = Range{Duration(10 * time.Millisecond), Duration(100 * time.Millisecond)}
			cfg.Faults.PausePerSec = 1
			cfg.Faults.PauseFor = Range{Duration(10 * time.Millisecond), Duration(100 * time.Millisecond)}
			cfg.Faults.IncludeWorkload = include
		}
	}
	onClient := func(res Result) bool {
		return slices.ContainsFunc(res.Faults, func(f Fault) bool { return f.Node == "client" && f.Kind == FaultCrash })
	}
	if res := livenessRunner(t, crashes(false)).Run(1, nil); onClient(res) {
		t.Fatal("the workload was crashed without include_workload")
	}
	r := livenessRunner(t, crashes(true))
	inFlight := false
	for seed := range uint64(4) {
		res := r.Run(seed, nil)
		if res.Failed || !res.Finished || !onClient(res) {
			t.Fatalf("seed %d: failed=%v finished=%v client crashes=%v %s", seed, res.Failed, res.Finished, onClient(res), res.Reason)
		}
		if !res.History.Checked || !res.History.Ok {
			t.Fatalf("seed %d: history checked=%v ok=%v", seed, res.History.Checked, res.History.Ok)
		}
		inFlight = inFlight || traced(res, "operations in flight, their outcome is unknown") >= 0
		replay := r.Run(seed, res.Faults)
		if diff, diverged := divergence(res.Trace, replay.Trace); diverged {
			t.Fatalf("seed %d: replay from the recorded faults diverges at %s", seed, diff)
		}
	}
	if !inFlight {
		t.Fatal("no client crash landed while an operation was in flight")
	}
}

func TestMultipleWorkloadsMustAllFinish(t *testing.T) {
	res := livenessRunner(t, func(cfg *Config, wasm string) {
		cfg.Workloads = []NodeSpec{cfg.Workload, {Name: "client2", Wasm: wasm, Args: []string{"-role", "client", "-writes", "40"}}}
		cfg.Workload = NodeSpec{}
	}).Run(1, nil)
	first, all := traced(res, "client#1 finished, waiting for the other workloads"), traced(res, "workload finished")
	if res.Failed || !res.Finished || first < 0 || all < first {
		t.Fatalf("failed=%v finished=%v first done at line %d, all done at line %d: %s", res.Failed, res.Finished, first, all, res.Reason)
	}
}

func TestFaultsFalseShieldsANode(t *testing.T) {
	off := false
	cfg := faultConfig()
	cfg.Nodes[0].Faults = &off
	cfg.Faults.PartitionTopologies = topologies
	cfg.Faults.ClogPerSec, cfg.Faults.SwizzlePerSec, cfg.Faults.ResetPerSec, cfg.Faults.SlowPerSec = 0.5, 0.2, 0.3, 0.3
	for seed := range uint64(30) {
		for _, f := range faultsFor(cfg, seed) {
			if f.Node == "n1" || f.From == "n1" || f.To == "n1" || slices.Contains(f.Side, "n1") || slices.Contains(slices.Concat(f.Groups...), "n1") {
				t.Fatalf("seed %d: shielded node n1 is in fault %+v", seed, f)
			}
		}
	}
	w := &world{cfg: cfg, shielded: map[string]bool{"n1": true}}
	w.applyNetFault(Fault{Kind: FaultPartition, Side: []string{"n2"}, Until: Duration(time.Hour)})
	if w.cut("n1", "n2") || !w.cut("n2", "n3") {
		t.Fatal("a partition must cut n2 from n3 but never touch the shielded n1")
	}
}

func TestWorkloadFaultTargets(t *testing.T) {
	on, off := true, false
	cfg := faultConfig()
	cfg.Finally = &NodeSpec{Name: "check"}
	if got := faultTargets(cfg); !slices.Equal(got, []string{"n1", "n2", "n3"}) {
		t.Fatalf("default fault targets %v, want the nodes only", got)
	}
	if got := nodeNames(cfg); !slices.Equal(got, []string{"n1", "n2", "n3", "client"}) {
		t.Fatalf("default network targets %v, want the nodes and the workload", got)
	}
	cfg.Faults.IncludeWorkload = true
	if got := faultTargets(cfg); !slices.Equal(got, []string{"n1", "n2", "n3", "client"}) {
		t.Fatalf("include_workload targets %v, want the workload too", got)
	}
	cfg.Faults.IncludeWorkload = false
	cfg.Workload.Faults, cfg.Nodes[2].Faults = &on, &off
	if got := faultTargets(cfg); !slices.Equal(got, []string{"n1", "n2", "client"}) {
		t.Fatalf("per-node faults targets %v, want n3 out and the workload in", got)
	}
}

func TestConfigValidatesWorkloadsAndLiveness(t *testing.T) {
	cfg := faultConfig()
	cfg.Nodes = []NodeSpec{{Name: "n1", Pkg: "./n"}}
	cfg.Workload = NodeSpec{}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "workload.name is required") {
		t.Fatalf("no workload: %v", err)
	}
	cfg.Workloads = []NodeSpec{{Name: "a", Pkg: "./w"}, {Name: "b", Pkg: "./w"}}
	cfg.Faults = FaultSpec{}
	if err := cfg.validate(); err != nil {
		t.Fatalf("workloads without workload: %v", err)
	}
	if got := cfg.workloads(); len(got) != 2 || got[0].Name != "a" {
		t.Fatalf("workloads %v", got)
	}
	cfg.Finally = &NodeSpec{Name: "a", Pkg: "./c"}
	if err := cfg.validate(); err == nil {
		t.Fatal("a finally check sharing a workload's name was accepted")
	}
	cfg.Finally = nil
	cfg.Liveness.Within = Duration(-time.Second)
	if err := cfg.validate(); err == nil {
		t.Fatal("a negative liveness.within was accepted")
	}
}

func TestHistoryCountsOpenOperationsPerInstance(t *testing.T) {
	h := &history{}
	put := []byte(`{"client":1,"kind":"put","key":"x","value":"1"}`)
	done := h.call(0, put, "client#1")
	h.call(1, put, "client#1")
	h.call(2, put, "client#2")
	h.complete(3, done, "", outcomeOk)
	if h.open("client#1") != 1 || h.open("client#2") != 1 || h.open("client#3") != 0 {
		t.Fatalf("open counts %d %d %d, want 1 1 0", h.open("client#1"), h.open("client#2"), h.open("client#3"))
	}
}

func TestCatalogFindsEventually(t *testing.T) {
	src := `package main

import "github.com/kunalgoesbyken/seedfall/sim"

func f() { sim.Eventually(func() bool { return true }, "leader elected", 0) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := fileProperties(fset, ".", file)
	want := []CatalogEntry{{Kind: Eventually, Message: "leader elected", Location: "main.go:5"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog %+v, want %+v", got, want)
	}
}
