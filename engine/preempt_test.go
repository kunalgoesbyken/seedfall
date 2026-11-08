package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
)

func TestPreemptSpecNormalize(t *testing.T) {
	tests := []struct {
		name string
		spec PreemptSpec
		want string
		err  string
	}{
		{"empty means none", PreemptSpec{}, StrategyNone, ""},
		{"interleave is random", PreemptSpec{Interleave: true, Strategy: StrategyNone}, StrategyRandom, ""},
		{"interleave with pct is rejected", PreemptSpec{Interleave: true, Strategy: StrategyPCT, Depth: 2, Steps: 10}, "", "shorthand"},
		{"unknown strategy", PreemptSpec{Strategy: "dfs"}, "", "unknown strategy"},
		{"pct needs depth", PreemptSpec{Strategy: StrategyPCT, Steps: 10}, "", "depth 0"},
		{"pct needs steps", PreemptSpec{Strategy: StrategyPCT, Depth: 2}, "", "steps 0"},
		{"calls stays inside the time slice", PreemptSpec{Strategy: StrategyCalls, Calls: preemptCalls + 1}, "", "outside"},
		{"calls ignores pct fields", PreemptSpec{Strategy: StrategyCalls, Calls: 100}, StrategyCalls, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.normalize()
			switch {
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Fatalf("normalize() = %v, want an error containing %q", err, tt.err)
			case tt.err == "" && err != nil:
				t.Fatalf("normalize() = %v", err)
			case tt.err == "" && tt.spec.Strategy != tt.want:
				t.Fatalf("strategy %q, want %q", tt.spec.Strategy, tt.want)
			}
		})
	}
}

func TestCountLoopsAddsTheCountdownAfterEveryLoopHeader(t *testing.T) {
	tick := countdown(7, 1)
	minInt64 := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}
	first := append(append([]byte{opLoop, blockEmpty, opI64Const}, minInt64...), opDrop, opI32Const, 0x7f, opBrTable, 2, 0, 1, 0, opPrefixFC, 10, 0, 0, opLoad, 2, 0x88, 0x01)
	second := []byte{opLoop, typeI32, opI32Const, 1, opBrIf, 0, opEnd, opF64Const, 1, 2, 3, 4, 5, 6, 7, 8, opDrop, opEnd, opEnd}
	body := append(slices.Clone(first), second...)
	got, err := countLoops(nil, body, tick)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Concat(first[:2], tick, first[2:], second[:2], tick, second[2:])
	if !bytes.Equal(got, want) {
		t.Fatalf("countLoops =\n% x\nwant\n% x", got, want)
	}
	if _, err := countLoops(nil, []byte{0xfd, 0x0c}, tick); err == nil || !strings.Contains(err.Error(), "0xfd") {
		t.Fatalf("a SIMD opcode gave %v, want an unsupported opcode error", err)
	}
	if _, err := countLoops(nil, []byte{opI32Const}, tick); err == nil {
		t.Fatal("a truncated body decoded")
	}
}

func TestInstrumentedLoopsCompile(t *testing.T) {
	raw, err := os.ReadFile(guestWasm(t))
	if err != nil {
		t.Fatal(err)
	}
	calls, err := instrument(raw, false, false)
	if err != nil {
		t.Fatal(err)
	}
	loops, err := instrument(raw, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(loops) <= len(calls) || len(calls) <= len(raw) {
		t.Fatalf("sizes raw %d, calls %d, loops %d: loop counting added nothing", len(raw), len(calls), len(loops))
	}
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)
	if _, err := rt.CompileModule(ctx, loops); err != nil {
		t.Fatalf("the loop-counted module does not compile: %v", err)
	}
}

func preemptRunner(t *testing.T, args []string, spec PreemptSpec) *Runner {
	t.Helper()
	wasm := guestWasm(t)
	cfg := defaultConfig()
	cfg.Workload = NodeSpec{Name: "client", Wasm: wasm, Args: args}
	cfg.Duration = Duration(10 * time.Second)
	cfg.Preempt = spec
	cfg.Dir = filepath.Dir(wasm)
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

func spinArgs(timeSlices int) []string {
	return []string{"-role", "spin", "-iters", strconv.Itoa(timeSlices * preemptCalls)}
}

func napArgs(naps, iters int) []string {
	return []string{"-role", "spin", "-naps", strconv.Itoa(naps), "-iters", strconv.Itoa(iters)}
}

func TestGuestCountedLoopsUseVirtualTime(t *testing.T) {
	timeSlices := 3
	args := spinArgs(timeSlices)
	without := preemptRunner(t, args, PreemptSpec{Strategy: StrategyNone}).Run(1, nil)
	with := preemptRunner(t, args, PreemptSpec{Strategy: StrategyNone, Loops: true}).Run(1, nil)
	if without.Failed || with.Failed {
		t.Fatalf("spin failed: %q / %q", without.Reason, with.Reason)
	}
	if without.Duration >= time.Millisecond {
		t.Fatalf("without loop counting the loop took %s of virtual time, want almost none", without.Duration)
	}
	if want := time.Duration(timeSlices) * time.Duration(preemptSlice); with.Duration < want {
		t.Fatalf("with loop counting the loop took %s of virtual time, want at least %s", with.Duration, want)
	}
}

func preemptFaults(faults []Fault) []Fault {
	return slices.DeleteFunc(slices.Clone(faults), func(f Fault) bool { return f.Kind != FaultPreempt })
}

func TestGuestPCTChangePointsReplayAndShrink(t *testing.T) {
	r := preemptRunner(t, []string{"-role", "race", "-writes", "100"}, PreemptSpec{Strategy: StrategyPCT, Depth: 4, Steps: 100000})
	var failed Result
	for seed := range uint64(10) {
		if res := r.Run(seed, nil); res.Failed {
			failed = res
			break
		}
	}
	if !failed.Failed {
		t.Fatal("no seed in 0-9 found the race under pct")
	}
	points := preemptFaults(failed.Faults)
	if len(points) == 0 || len(points) > 3 {
		t.Fatalf("seed %d recorded change points %v, want 1 to 3", failed.Seed, points)
	}
	replay := r.Run(failed.Seed, failed.Faults)
	if diff, diverged := divergence(failed.Trace, replay.Trace); diverged {
		t.Fatalf("replaying the recorded change points diverges at %s", diff)
	}
	if res := r.Run(failed.Seed, []Fault{}); res.Failed || len(preemptFaults(res.Faults)) > 0 {
		t.Fatalf("an empty schedule failed (%q) or preempted %v", res.Reason, preemptFaults(res.Faults))
	}
	shrunk := Shrink(r, failed, ShrinkOptions{Jobs: 2})
	if !shrunk.Result.sameFailure(failed) || len(preemptFaults(shrunk.Schedule.Faults)) != 1 {
		t.Fatalf("shrunk to %v failing with %q", shrunk.Schedule.Faults, shrunk.Result.Reason)
	}
	if err := r.cfg.CheckSchedule(shrunk.Schedule); err != nil {
		t.Fatal(err)
	}
	r.cfg.Preempt.Strategy = StrategyRandom
	if err := r.cfg.CheckSchedule(shrunk.Schedule); err == nil || !strings.Contains(err.Error(), "preempt.strategy pct") {
		t.Fatalf("a preempt fault under strategy random checked as %v", err)
	}
}

func TestGuestCallsStrategyPreemptsEveryNCalls(t *testing.T) {
	args := []string{"-role", "race", "-writes", "100"}
	if res := preemptRunner(t, args, PreemptSpec{Strategy: StrategyNone}).Run(1, nil); res.Failed {
		t.Fatalf("without preemption the race fired: %s", res.Reason)
	}
	r := preemptRunner(t, args, PreemptSpec{Strategy: StrategyCalls, Calls: 2000})
	a, b := r.Run(1, nil), r.Run(1, nil)
	if !a.Failed || !slices.Equal(a.Trace, b.Trace) {
		t.Fatalf("preempting every 2000 calls gave %q and %q, want the same failure twice", a.Reason, b.Reason)
	}
	if !strings.Contains(a.Trace[0], "every 2000 counted function calls") {
		t.Fatalf("trace starts %q, want the strategy", a.Trace[0])
	}
}

func TestGuestPCTChangePointsFireAcrossParks(t *testing.T) {
	timeSlices, depth := 3, 4
	spec := PreemptSpec{Strategy: StrategyPCT, Depth: depth, Steps: int64(timeSlices * preemptCalls), Loops: true}
	r := preemptRunner(t, spinArgs(timeSlices), spec)
	res := r.Run(1, nil)
	points := preemptFaults(res.Faults)
	if res.Failed || len(points) != depth-1 {
		t.Fatalf("failed %v (%q), change points %v, want %d", res.Failed, res.Reason, points, depth-1)
	}
	for _, f := range points {
		if parked := time.Duration(f.Op/preemptCalls) * time.Duration(preemptSlice); time.Duration(f.At) < parked {
			t.Fatalf("change point at counted call %d fired at %s, before the %s its earlier time slices park for", f.Op, time.Duration(f.At), parked)
		}
	}
	replay := r.Run(1, res.Faults)
	if !reflect.DeepEqual(preemptFaults(replay.Faults), points) {
		t.Fatalf("replay fired %v, want %v", preemptFaults(replay.Faults), points)
	}
}

func TestGuestPCTChangePointsFireAcrossSleeps(t *testing.T) {
	naps, depth := 16, 8
	iters := preemptCalls / naps
	spec := PreemptSpec{Strategy: StrategyPCT, Depth: depth, Steps: int64(naps * iters), Loops: true}
	r := preemptRunner(t, napArgs(naps, iters), spec)
	res := r.Run(1, nil)
	points := preemptFaults(res.Faults)
	if res.Failed || len(points) != depth-1 {
		t.Fatalf("failed %v (%q), change points %v, want %d", res.Failed, res.Reason, points, depth-1)
	}
	if last := points[len(points)-1]; last.At == 0 {
		t.Fatalf("every change point fired at t=0, so none came after a sleep: %v", points)
	}
	replay := r.Run(1, res.Faults)
	if !reflect.DeepEqual(preemptFaults(replay.Faults), points) {
		t.Fatalf("replay fired %v, want %v", preemptFaults(replay.Faults), points)
	}
}
