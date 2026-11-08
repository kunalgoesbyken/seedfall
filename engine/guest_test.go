package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var guest struct {
	once sync.Once
	dir  string
	wasm string
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if guest.dir != "" {
		os.RemoveAll(guest.dir)
	}
	os.Exit(code)
}

func guestWasm(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a wasip1 guest")
	}
	guest.once.Do(func() {
		if guest.dir, guest.err = os.MkdirTemp("", "seedfall-guest"); guest.err != nil {
			return
		}
		guest.wasm = filepath.Join(guest.dir, "guest.wasm")
		cmd := exec.Command("go", "build", "-o", guest.wasm, "./testdata/guest")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if out, err := cmd.CombinedOutput(); err != nil {
			guest.err = fmt.Errorf("build guest: %w\n%s", err, out)
		}
	})
	if guest.err != nil {
		t.Fatal(guest.err)
	}
	return guest.wasm
}

func guestRunner(t *testing.T, fsync bool) *Runner {
	t.Helper()
	r, err := NewRunner(guestConfig(t, fsync))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.rt.Close(context.Background()) })
	return r
}

func guestConfig(t *testing.T, fsync bool) *Config {
	t.Helper()
	wasm := guestWasm(t)
	cfg := defaultConfig()
	durable := "-fsync=true"
	if !fsync {
		durable = "-fsync=false"
	}
	cfg.Nodes = []NodeSpec{{Name: "store", Wasm: wasm, Args: []string{"-role", "store", durable}}}
	cfg.Workload = NodeSpec{Name: "client", Wasm: wasm, Args: []string{"-role", "client"}}
	cfg.Duration = Duration(10 * time.Second)
	cfg.Faults.CrashPerSec = 2
	cfg.Faults.RestartAfter = Range{Duration(10 * time.Millisecond), Duration(100 * time.Millisecond)}
	cfg.Faults.PartitionPerSec = 1
	cfg.Faults.PartitionFor = Range{Duration(20 * time.Millisecond), Duration(200 * time.Millisecond)}
	cfg.Faults.TornWrites = true
	cfg.Faults.ReorderWrites = true
	cfg.Faults.CrashOnIO = 0.02
	cfg.Faults.PausePerSec = 0.5
	cfg.Faults.PauseFor = Range{Duration(10 * time.Millisecond), Duration(100 * time.Millisecond)}
	cfg.Dir = filepath.Dir(wasm)
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

type outcome struct {
	Failed   bool
	Reason   string
	Failure  Failure
	Finished bool
	Steps    int
	Duration time.Duration
	Faults   []Fault
	Trace    []string
}

func outcomeOf(r Result) outcome {
	return outcome{r.Failed, r.Reason, r.Failure, r.Finished, r.Steps, r.Duration, r.Faults, r.Trace}
}

func TestGuestRunsAreByteIdentical(t *testing.T) {
	r := guestRunner(t, true)
	for seed := range uint64(4) {
		a, b := r.Run(seed, nil), r.Run(seed, nil)
		if diff, diverged := divergence(a.Trace, b.Trace); diverged {
			t.Fatalf("seed %d diverges at %s", seed, diff)
		}
		if !reflect.DeepEqual(outcomeOf(a), outcomeOf(b)) {
			t.Fatalf("seed %d: two runs differ:\n%+v\n%+v", seed, outcomeOf(a), outcomeOf(b))
		}
		if a.Failed {
			t.Fatalf("seed %d: the fsync store failed: %s", seed, a.Reason)
		}
		if len(a.Trace) == 0 || len(a.FaultsApplied()) == 0 {
			t.Fatalf("seed %d ran %d trace lines and %d faults; the test exercises nothing", seed, len(a.Trace), len(a.FaultsApplied()))
		}
	}
	if slices.Equal(r.Run(1, nil).Trace, r.Run(2, nil).Trace) {
		t.Fatal("seeds 1 and 2 produced the same trace")
	}
}

func TestGuestDoublecheckPasses(t *testing.T) {
	r := guestRunner(t, true)
	r.Doublecheck = true
	if res := r.Run(3, nil); res.Failed {
		t.Fatalf("doublecheck flagged a deterministic run: %s", res.Reason)
	}
}

func TestGuestNumericAssertionsEventsAndHashReplay(t *testing.T) {
	r := guestRunner(t, true)
	a, b := r.Run(3, nil), r.Run(3, nil)
	p := a.Properties["a fault cost an acknowledgement"]
	if p == nil || p.Margin == nil || p.Kind != Sometimes {
		t.Fatalf("numeric property %+v", p)
	}
	if len(a.Events) != 1 || a.Events[0].Name != "writes finished" || !strings.Contains(a.Events[0].Details, `"writes":60`) {
		t.Fatalf("events %+v", a.Events)
	}
	if !slices.ContainsFunc(a.Trace, func(l string) bool { return strings.Contains(l, "event writes finished from client#") }) {
		t.Fatal("the event is not in the trace")
	}
	if a.HostCalls == 0 || a.Hash() != b.Hash() || !reflect.DeepEqual(a.Events, b.Events) || *p.Margin != *b.Properties["a fault cost an acknowledgement"].Margin {
		t.Fatalf("replay differs: %d calls, hashes %s and %s", a.HostCalls, a.Hash(), b.Hash())
	}
	if r.Run(4, nil).Hash() == a.Hash() {
		t.Fatal("seeds 3 and 4 have the same determinism hash")
	}
}

func TestGuestBudgetCapsTheWorkloadAndShortensTheRun(t *testing.T) {
	r := guestRunner(t, true)
	full := r.Run(3, nil)
	if full.Ops != 60 {
		t.Fatalf("the client asked sim.Budget for %d operations, want 60", full.Ops)
	}
	capped := r.Replay(3, Schedule{Faults: []Fault{}, Ops: 5, Duration: Duration(time.Second)})
	if capped.Ops != 60 || capped.Horizon != time.Second {
		t.Fatalf("capped run reports %d ops wanted and a %s horizon", capped.Ops, capped.Horizon)
	}
	if !slices.ContainsFunc(capped.Trace, func(l string) bool { return strings.Contains(l, "sim.Budget capped at 5 of 60 operations") }) {
		t.Fatalf("the trace does not show the cap:\n%s", strings.Join(capped.Trace, "\n"))
	}
	if again := r.Replay(3, Schedule{Faults: []Fault{}, Ops: 5, Duration: Duration(time.Second)}); !reflect.DeepEqual(again.Trace, capped.Trace) {
		t.Fatal("a capped schedule does not replay exactly")
	}
	if !capped.Finished || capped.Steps >= full.Steps {
		t.Fatalf("5 of 60 writes took %d steps (finished %v), the full run %d", capped.Steps, capped.Finished, full.Steps)
	}
}

func TestGuestSweepDoesNotDependOnParallelism(t *testing.T) {
	r := guestRunner(t, false)
	failing := func(jobs int) []uint64 {
		var seeds []uint64
		for _, f := range Sweep(r, 1, 8, SweepOptions{Jobs: jobs, KeepGoing: true}).Failures {
			seeds = append(seeds, f.Seed)
		}
		slices.Sort(seeds)
		return seeds
	}
	serial, parallel := failing(1), failing(4)
	if !slices.Equal(serial, parallel) {
		t.Fatalf("failing seeds with 1 job %v, with 4 jobs %v", serial, parallel)
	}
	if len(serial) == 0 {
		t.Fatal("the store without fsync never lost a write; the faults are not reaching the disk")
	}
}

func firstFailure(t *testing.T, r *Runner) Result {
	t.Helper()
	for seed := range uint64(20) {
		if res := r.Run(seed, nil); res.Failed {
			return res
		}
	}
	t.Fatal("no seed in 0-19 fails")
	return Result{}
}

func TestGuestFailureReplaysFromItsSchedule(t *testing.T) {
	r := guestRunner(t, false)
	failed := firstFailure(t, r)
	replay := r.Run(failed.Seed, failed.Faults)
	if diff, diverged := divergence(failed.Trace, replay.Trace); diverged {
		t.Fatalf("replaying seed %d from its fault schedule diverges at %s", failed.Seed, diff)
	}
	if !replay.sameFailure(failed) || replay.Reason != failed.Reason {
		t.Fatalf("replay failed with %q, want %q", replay.Reason, failed.Reason)
	}
}

func TestGuestShrinkKeepsTheFailureAndIsMinimal(t *testing.T) {
	r := guestRunner(t, false)
	failed := firstFailure(t, r)
	shrunk := Shrink(r, failed, ShrinkOptions{Jobs: 2})
	best := shrunk.Result
	if !best.sameFailure(failed) || len(best.Faults) > len(failed.Faults) {
		t.Fatalf("shrunk to %d faults failing with %q, from %d failing with %q", len(best.Faults), best.Reason, len(failed.Faults), failed.Reason)
	}
	if again := r.Replay(best.Seed, shrunk.Schedule); !reflect.DeepEqual(again.Trace, best.Trace) {
		t.Fatal("the shrunk schedule does not replay exactly")
	}
	for i := range shrunk.Schedule.Faults {
		without := shrunk.Schedule
		without.Faults = slices.Delete(slices.Clone(without.Faults), i, i+1)
		if r.Replay(best.Seed, without).sameFailure(failed) {
			t.Fatalf("removing %s still fails the same way", shrunk.Schedule.Faults[i].Describe())
		}
	}
}

func TestGuestBuggifyFiringsReplayExactly(t *testing.T) {
	r := guestRunner(t, true)
	r.cfg.Faults.Buggify = true
	fired := 0
	for seed := range uint64(8) {
		a := r.Run(seed, nil)
		if a.Failed {
			t.Fatalf("seed %d: the fsync store failed: %s", seed, a.Reason)
		}
		var firings []Fault
		for _, f := range a.Faults {
			if f.Kind == FaultBuggify {
				firings = append(firings, f)
			}
		}
		fired += len(firings)
		if s := a.Buggify["store drops the reply"]; len(firings) > 0 && (s == nil || !s.Enabled || s.Fired != len(firings)) {
			t.Fatalf("seed %d: %d firings recorded but site is %+v", seed, len(firings), s)
		}
		replay := r.Run(seed, a.Faults)
		if diff, diverged := divergence(a.Trace, replay.Trace); diverged {
			t.Fatalf("seed %d: replaying the recorded schedule diverges at %s", seed, diff)
		}
		if len(firings) > 0 && slices.ContainsFunc(r.Run(seed, []Fault{}).Faults, func(f Fault) bool { return f.Kind == FaultBuggify }) {
			t.Fatalf("seed %d: an empty schedule still fired a buggify site", seed)
		}
	}
	if fired == 0 {
		t.Fatal("no buggify site fired in 8 seeds")
	}
}

func TestGuestNetworkFaultsReplayExactly(t *testing.T) {
	r := guestRunner(t, true)
	f := &r.cfg.Faults
	f.PartitionTopologies = []string{TopologyHalf, TopologyOne}
	f.ClogPerSec, f.SwizzlePerSec, f.ResetPerSec, f.SlowPerSec = 2, 0.5, 1, 1
	kinds := map[string]bool{}
	for seed := range uint64(4) {
		a := r.Run(seed, nil)
		if a.Failed {
			t.Fatalf("seed %d: the fsync store failed under network faults: %s", seed, a.Reason)
		}
		if diff, diverged := divergence(a.Trace, r.Run(seed, nil).Trace); diverged {
			t.Fatalf("seed %d: two runs diverge at %s", seed, diff)
		}
		if diff, diverged := divergence(a.Trace, r.Run(seed, a.Faults).Trace); diverged {
			t.Fatalf("seed %d: replaying the schedule diverges at %s", seed, diff)
		}
		for _, f := range a.FaultsApplied() {
			kinds[f.Kind] = true
		}
	}
	for _, k := range []string{FaultClog, FaultReset, FaultSlow, FaultPartition} {
		if !kinds[k] {
			t.Errorf("no seed applied a %s", k)
		}
	}
}

func diskKinds(res Result) map[string]bool {
	kinds := map[string]bool{}
	for _, f := range res.FaultsApplied() {
		kinds[f.Kind] = true
	}
	return kinds
}

func TestGuestDiskFaultsReplayExactly(t *testing.T) {
	r := guestRunner(t, true)
	f := &r.cfg.Faults
	f.WriteEIO, f.WriteENOSPC, f.ReadEIO, f.ReadCorrupt, f.LostWrite, f.BitrotPerSec = 0.03, 0.03, 0.05, 0.3, 0.05, 1
	f.CorruptOnCrash, f.DiskLatency = true, Range{Duration(time.Millisecond), Duration(5 * time.Millisecond)}
	kinds := map[string]bool{}
	for seed := range uint64(6) {
		a := r.Run(seed, nil)
		if diff, diverged := divergence(a.Trace, r.Run(seed, nil).Trace); diverged {
			t.Fatalf("seed %d: two runs diverge at %s", seed, diff)
		}
		if diff, diverged := divergence(a.Trace, r.Run(seed, a.Faults).Trace); diverged {
			t.Fatalf("seed %d: replaying the recorded schedule diverges at %s", seed, diff)
		}
		for k := range diskKinds(a) {
			kinds[k] = true
		}
		if p := a.Properties["a write failed with ENOSPC"]; diskKinds(a)[FaultENOSPC] && (p == nil || p.True == 0) {
			t.Fatalf("seed %d: a write_enospc fault did not reach the guest as ENOSPC", seed)
		}
	}
	for _, k := range []string{FaultWriteEIO, FaultENOSPC, FaultReadEIO, FaultReadCorrupt, FaultLostWrite, FaultBitrot} {
		if !kinds[k] {
			t.Errorf("no seed recorded a %s", k)
		}
	}
}

func TestGuestDiskFullReachesTheProgramAsENOSPC(t *testing.T) {
	r := guestRunner(t, true)
	r.cfg.Faults = defaultConfig().Faults
	r.cfg.Faults.DiskBytes = 40
	res := r.Run(1, nil)
	if p := res.Properties["a write failed with ENOSPC"]; p == nil || p.True == 0 {
		t.Fatalf("the guest never saw ENOSPC: %+v (%s)", p, res.Reason)
	}
	if res.Failed {
		t.Fatalf("a full disk failed the run: %s", res.Reason)
	}
}

func TestGuestLostWriteShrinksToTheLostWrite(t *testing.T) {
	r := guestRunner(t, true)
	r.cfg.Faults.LostWrite = 0.2
	failed := firstFailure(t, r)
	shrunk := Shrink(r, failed, ShrinkOptions{Jobs: 2})
	best := shrunk.Result
	if !best.sameFailure(failed) || !diskKinds(best)[FaultLostWrite] {
		t.Fatalf("shrunk to %v failing with %q", best.Faults, best.Reason)
	}
	if again := r.Replay(best.Seed, shrunk.Schedule); !reflect.DeepEqual(again.Trace, best.Trace) {
		t.Fatal("the shrunk schedule does not replay exactly")
	}
}

func TestGuestLinksSymlinksAndTimes(t *testing.T) {
	wasm := guestWasm(t)
	cfg := defaultConfig()
	cfg.Workload = NodeSpec{Name: "client", Wasm: wasm, Args: []string{"-role", "links"}}
	cfg.Duration = Duration(time.Second)
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.rt.Close(context.Background()) })
	if res := r.Run(1, nil); res.Failed {
		t.Fatalf("guest failed: %s\n%s", res.Reason, strings.Join(res.Trace, "\n"))
	}
}
