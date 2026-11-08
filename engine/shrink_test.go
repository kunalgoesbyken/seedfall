package engine

import (
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

type fakeReplayer struct {
	fails func(Schedule) (Failure, string)
	ops   int64
	runs  atomic.Int64
}

func (f *fakeReplayer) Replay(seed uint64, s Schedule) Result {
	f.runs.Add(1)
	res := Result{Seed: seed, Faults: s.Faults, Horizon: time.Duration(s.Duration), Ops: f.ops}
	res.Failure, res.Reason = f.fails(s)
	res.Failed = res.Reason != ""
	return res
}

func (f *fakeReplayer) original(t *testing.T, faults []Fault) Result {
	t.Helper()
	res := f.Replay(1, Schedule{Faults: faults, Duration: Duration(time.Minute)})
	if !res.Failed {
		t.Fatal("the full schedule must fail")
	}
	return res
}

func schedule(n int) []Fault {
	out := make([]Fault, n)
	for i := range out {
		out[i] = Fault{At: Duration(time.Duration(i+1) * time.Second), Kind: FaultCrash, Node: fmt.Sprintf("n%d", i+1)}
	}
	return out
}

func present(s Schedule) map[string]Fault {
	out := map[string]Fault{}
	for _, f := range s.Faults {
		out[f.Node] = f
	}
	return out
}

func has(s Schedule, nodes ...string) int {
	p := present(s)
	n := 0
	for _, node := range nodes {
		if _, ok := p[node]; ok {
			n++
		}
	}
	return n
}

func lost(n int) (Failure, string) {
	return Failure{Class: FailAssert, Node: "client"}, fmt.Sprintf("acknowledged write lost: k%d", n)
}

func TestShrinkReturnsAOneMinimalScheduleWithTheSameFailure(t *testing.T) {
	tests := []struct {
		name  string
		fails func(Schedule) (Failure, string)
		want  int
	}{
		{"two faults are both needed", func(s Schedule) (Failure, string) {
			if has(s, "n2", "n5") == 2 {
				return lost(len(s.Faults))
			}
			return Failure{}, ""
		}, 2},
		{"any two of three faults are enough", func(s Schedule) (Failure, string) {
			if n := has(s, "n1", "n3", "n6"); n >= 2 {
				return lost(n)
			}
			return Failure{}, ""
		}, 2},
		{"a different failure is not accepted", func(s Schedule) (Failure, string) {
			switch {
			case has(s, "n4", "n7") == 2:
				return lost(len(s.Faults))
			case has(s, "n4") == 1:
				return Failure{Class: FailCrash, Node: "n1"}, "n1 crashed: panic"
			}
			return Failure{}, ""
		}, 2},
		{"a failure with no faults shrinks to nothing", func(s Schedule) (Failure, string) {
			return lost(len(s.Faults))
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeReplayer{fails: tt.fails}
			original := f.original(t, schedule(8))
			var progress []int
			shrunk := Shrink(f, original, ShrinkOptions{Progress: func(p ShrinkStep) { progress = append(progress, p.Faults) }})
			best := shrunk.Schedule
			if len(best.Faults) != tt.want || !shrunk.Result.sameFailure(original) {
				t.Fatalf("shrunk to %d faults (%q), want %d with the original failure", len(best.Faults), shrunk.Result.Reason, tt.want)
			}
			if !slices.IsSortedFunc(progress, func(a, b int) int { return b - a }) {
				t.Fatalf("progress %v is not decreasing", progress)
			}
			if shrunk.Before.Faults != 8 || shrunk.After.Faults != tt.want || shrunk.Runs == 0 || shrunk.Exhausted {
				t.Fatalf("sizes %+v -> %+v after %d runs, exhausted %v", shrunk.Before, shrunk.After, shrunk.Runs, shrunk.Exhausted)
			}
			for i := range best.Faults {
				without := best
				without.Faults = slices.Delete(slices.Clone(best.Faults), i, i+1)
				if f.Replay(1, without).sameFailure(original) {
					t.Fatalf("removing fault %v still fails the same way, so %v is not 1-minimal", best.Faults[i], best.Faults)
				}
			}
		})
	}
}

func TestShrinkRemovesHalvesBeforeSingleFaults(t *testing.T) {
	f := &fakeReplayer{fails: func(s Schedule) (Failure, string) {
		if has(s, "n3") == 1 {
			return lost(len(s.Faults))
		}
		return Failure{}, ""
	}}
	shrunk := Shrink(f, f.original(t, schedule(64)), ShrinkOptions{})
	if len(shrunk.Schedule.Faults) != 1 {
		t.Fatalf("shrunk to %v", shrunk.Schedule.Faults)
	}
	if shrunk.Runs >= 64 {
		t.Fatalf("one needed fault out of 64 took %d runs, no better than removing one at a time", shrunk.Runs)
	}
}

func TestShrinkReducesFaultValues(t *testing.T) {
	base := []Fault{
		{At: Duration(3 * time.Second), Kind: FaultCrash, Node: "crash", Until: Duration(5 * time.Second)},
		{At: Duration(4 * time.Second), Kind: FaultClockJump, Node: "jump", Shift: Duration(-2 * time.Second)},
		{At: Duration(6 * time.Second), Kind: FaultPartition, Node: "part", Side: []string{"n1", "n2", "n3"}, Until: Duration(8 * time.Second)},
		{At: Duration(7 * time.Second), Kind: FaultCrashIO, Node: "io", Op: 9, Until: Duration(7*time.Second + 400*time.Millisecond)},
	}
	f := &fakeReplayer{fails: func(s Schedule) (Failure, string) {
		p := present(s)
		crash, jump, part, io := p["crash"], p["jump"], p["part"], p["io"]
		switch {
		case len(p) != 4:
			return Failure{}, ""
		case crash.At < Duration(time.Second) || crash.Until-crash.At < Duration(300*time.Millisecond):
			return Failure{}, ""
		case jump.Shift > Duration(-50*time.Millisecond):
			return Failure{}, ""
		case !slices.Contains(part.Side, "n2"):
			return Failure{}, ""
		case io.Until-io.At < Duration(20*time.Millisecond):
			return Failure{}, ""
		}
		return lost(len(s.Faults))
	}}
	shrunk := Shrink(f, f.original(t, base), ShrinkOptions{})
	p := present(shrunk.Schedule)
	near := func(name string, got, want Duration) {
		if got < want || got-want >= Duration(2*shrinkUnit) {
			t.Errorf("%s = %s, want just above %s", name, time.Duration(got), time.Duration(want))
		}
	}
	near("crash downtime", p["crash"].Until-p["crash"].At, Duration(300*time.Millisecond))
	near("crash time", p["crash"].At, Duration(time.Second))
	near("clock jump", -p["jump"].Shift, Duration(50*time.Millisecond))
	near("crash_io downtime", p["io"].Until-p["io"].At, Duration(20*time.Millisecond))
	if p["jump"].At != 0 || p["part"].At != 0 || p["part"].Until != 0 {
		t.Errorf("faults whose timing does not matter were not moved to 0: %v", shrunk.Schedule.Faults)
	}
	if !slices.Equal(p["part"].Side, []string{"n2"}) {
		t.Errorf("partition side %v, want [n2]", p["part"].Side)
	}
	if p["io"].At != base[3].At || p["io"].Op != 9 {
		t.Errorf("a fault recorded at run time moved: %v", p["io"])
	}
	if !slices.IsSortedFunc(shrunk.Schedule.Faults, func(a, b Fault) int { return int(a.At - b.At) }) {
		t.Errorf("the schedule is not in time order: %v", shrunk.Schedule.Faults)
	}
}

func TestShrinkTrimsTheDurationAndTheWorkload(t *testing.T) {
	f := &fakeReplayer{ops: 200, fails: func(s Schedule) (Failure, string) {
		if s.Duration < Duration(2*time.Second) || (s.Ops > 0 && s.Ops < 37) {
			return Failure{}, ""
		}
		return lost(int(s.Ops))
	}}
	shrunk := Shrink(f, f.original(t, nil), ShrinkOptions{})
	if shrunk.Schedule.Ops != 37 {
		t.Errorf("ops limit %d, want 37", shrunk.Schedule.Ops)
	}
	if d := shrunk.Schedule.Duration; d < Duration(2*time.Second) || d-Duration(2*time.Second) >= Duration(2*shrinkUnit) {
		t.Errorf("duration %s, want just above 2s", time.Duration(d))
	}
	if shrunk.Before != (ScheduleSize{Duration: Duration(time.Minute), Ops: 200}) || shrunk.After.Ops != 37 {
		t.Errorf("sizes %v -> %v", shrunk.Before, shrunk.After)
	}
}

func TestShrinkLeavesTheWorkloadAloneWhenItHasNoBudget(t *testing.T) {
	f := &fakeReplayer{fails: func(Schedule) (Failure, string) { return lost(0) }}
	if shrunk := Shrink(f, f.original(t, nil), ShrinkOptions{}); shrunk.Schedule.Ops != 0 || shrunk.After.Ops != 0 {
		t.Fatalf("a workload that never called sim.Budget got an ops limit: %+v", shrunk.Schedule)
	}
}

func TestShrinkIsTheSameWithAnyNumberOfJobs(t *testing.T) {
	fails := func(s Schedule) (Failure, string) {
		p := present(s)
		if has(s, "n3", "n9", "n17") >= 2 && p["n9"].Until-p["n9"].At >= Duration(time.Second) {
			return lost(len(s.Faults))
		}
		return Failure{}, ""
	}
	faults := schedule(24)
	for i := range faults {
		faults[i].Until = faults[i].At + Duration(3*time.Second)
	}
	var want Shrunk
	for _, jobs := range []int{1, 3, 8} {
		f := &fakeReplayer{fails: fails}
		got := Shrink(f, f.original(t, faults), ShrinkOptions{Jobs: jobs})
		if jobs == 1 {
			want = got
			continue
		}
		if fmt.Sprint(got.Schedule) != fmt.Sprint(want.Schedule) || got.Runs != want.Runs {
			t.Fatalf("-j %d gave %v in %d runs, -j 1 gave %v in %d runs", jobs, got.Schedule, got.Runs, want.Schedule, want.Runs)
		}
	}
}

func TestShrinkStopsAtItsBudget(t *testing.T) {
	f := &fakeReplayer{fails: func(s Schedule) (Failure, string) {
		if has(s, "n2", "n5") == 2 {
			return lost(len(s.Faults))
		}
		return Failure{}, ""
	}}
	original := f.original(t, schedule(32))
	f.runs.Store(0)
	shrunk := Shrink(f, original, ShrinkOptions{MaxRuns: 5, Jobs: 4})
	if shrunk.Runs != 5 || !shrunk.Exhausted || !shrunk.Result.sameFailure(original) {
		t.Fatalf("budget of 5 runs: %d runs, exhausted %v, %q", shrunk.Runs, shrunk.Exhausted, shrunk.Result.Reason)
	}
	if f.runs.Load() > 5*4 {
		t.Fatalf("%d runs started with a budget of 5 on 4 jobs", f.runs.Load())
	}
	if len(shrunk.Schedule.Faults) >= 32 {
		t.Fatalf("no progress within the budget: %d faults", len(shrunk.Schedule.Faults))
	}
	slow := &fakeReplayer{fails: func(s Schedule) (Failure, string) {
		time.Sleep(time.Millisecond)
		return f.fails(s)
	}}
	timed := Shrink(slow, original, ShrinkOptions{MaxTime: time.Millisecond})
	if !timed.Exhausted || timed.Runs != 1 || !timed.Result.sameFailure(original) {
		t.Fatalf("a time budget shorter than one run gave %d runs, exhausted %v, %q", timed.Runs, timed.Exhausted, timed.Result.Reason)
	}
}

func TestBisectFindsTheSmallestPassingValue(t *testing.T) {
	for _, threshold := range []int64{0, 1, 37, 99, 100} {
		got := int64(100)
		bisect(0, 100, 1, func(v int64) bool {
			if v >= threshold {
				got = min(got, v)
				return true
			}
			return false
		})
		if got != threshold {
			t.Errorf("threshold %d: smallest found %d", threshold, got)
		}
	}
}

func TestSameFailureIgnoresNumbersOnly(t *testing.T) {
	base := Result{Failed: true, Failure: Failure{Class: FailAssert, Node: "client"}, Reason: "lost k12 after 3 crashes"}
	tests := []struct {
		name  string
		other Result
		want  bool
	}{
		{"different numbers", Result{Failed: true, Failure: base.Failure, Reason: "lost k7 after 41 crashes"}, true},
		{"different words", Result{Failed: true, Failure: base.Failure, Reason: "stale k12 after 3 crashes"}, false},
		{"different node", Result{Failed: true, Failure: Failure{Class: FailAssert, Node: "server"}, Reason: base.Reason}, false},
		{"different class", Result{Failed: true, Failure: Failure{Class: FailCrash, Node: "client"}, Reason: base.Reason}, false},
		{"passing run", Result{Failure: base.Failure, Reason: base.Reason}, false},
	}
	for _, tt := range tests {
		if got := tt.other.sameFailure(base); got != tt.want {
			t.Errorf("%s: sameFailure = %v, want %v", tt.name, got, tt.want)
		}
	}
}
