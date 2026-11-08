package engine

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func feedbackRunner(t *testing.T, fsync bool) *Runner {
	t.Helper()
	cfg := guestConfig(t, fsync)
	cfg.Feedback = true
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.rt.Close(context.Background()) })
	return r
}

func scheduleRoundTrip(t *testing.T, s Schedule) Schedule {
	t.Helper()
	b, err := json.Marshal(ScheduleFile{Seed: 1, Schedule: s})
	if err != nil {
		t.Fatal(err)
	}
	var file ScheduleFile
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	return file.Schedule
}

func TestGuestBranchReplaysFromItsSchedule(t *testing.T) {
	r := guestRunner(t, false)
	for seed := uint64(1); seed <= 4; seed++ {
		res := r.Branch(seed, 400*time.Millisecond, 7)
		if !res.Scheduled || len(res.Branches) != 1 {
			t.Fatalf("seed %d: scheduled %v, branches %v", seed, res.Scheduled, res.Branches)
		}
		replay := r.Replay(seed, scheduleRoundTrip(t, res.Schedule(Schedule{})))
		if diff, diverged := divergence(res.Trace, replay.Trace); diverged {
			t.Fatalf("seed %d: replaying a branch from its schedule diverges at %s", seed, diff)
		}
	}
}

func TestGuestChainedBranchesReplay(t *testing.T) {
	r := guestRunner(t, false)
	first := r.Replay(2, Schedule{Branches: []Branch{{At: Duration(200 * time.Millisecond), Key: 3, reroll: true}, {At: Duration(500 * time.Millisecond), Key: 9, reroll: true}}})
	if diff, diverged := divergence(first.Trace, r.Replay(2, scheduleRoundTrip(t, first.Schedule(Schedule{}))).Trace); diverged {
		t.Fatalf("a chained branch does not replay: %s", diff)
	}
}

func TestGuestStreamBranchReplays(t *testing.T) {
	r := guestRunner(t, false)
	base := r.Replay(5, Schedule{})
	res := r.Replay(5, Schedule{Branches: []Branch{{At: Duration(300 * time.Millisecond), Key: 11, Stream: "sched"}}})
	if !res.Scheduled {
		t.Fatal("a branched run is not marked scheduled")
	}
	if slices.Equal(base.Trace, res.Trace) {
		t.Fatal("re-keying the scheduler stream changed nothing")
	}
	if diff, diverged := divergence(res.Trace, r.Replay(5, scheduleRoundTrip(t, res.Schedule(Schedule{}))).Trace); diverged {
		t.Fatalf("a stream branch does not replay: %s", diff)
	}
}

func TestGuestFeedbackDoesNotChangeTheRun(t *testing.T) {
	plain, fb := guestRunner(t, false), feedbackRunner(t, false)
	for seed := uint64(1); seed <= 3; seed++ {
		a, b := plain.Replay(seed, Schedule{}), fb.Replay(seed, Schedule{})
		if !reflect.DeepEqual(outcomeOf(a), outcomeOf(b)) {
			t.Fatalf("seed %d: feedback instrumentation changed the run", seed)
		}
		if len(b.Functions) == 0 || len(b.Signals) == 0 {
			t.Fatalf("seed %d: no function bitmap (%d nodes) or signals (%d)", seed, len(b.Functions), len(b.Signals))
		}
		for _, name := range []string{"store", "client"} {
			if len(b.Functions[name]) == 0 {
				t.Errorf("seed %d: no bitmap for node %s", seed, name)
			}
		}
		if len(a.Signals) != 0 || a.Functions != nil {
			t.Fatalf("seed %d: a run without feedback reports signals", seed)
		}
	}
}

func growthOf(points []GrowthPoint) []GrowthPoint {
	out := slices.Clone(points)
	for i := range out {
		out[i].Elapsed = 0
	}
	return out
}

func TestGuestExploreIsTheSameAtAnyJobCount(t *testing.T) {
	r := feedbackRunner(t, true)
	opts := ExploreOptions{Lo: 1, Hi: 4, Rounds: 3, Batch: 4, Seed: 9, KeepGoing: true}
	opts.Jobs, opts.Corpus = 1, t.TempDir()
	one, err := Explore(r, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Jobs, opts.Corpus = 4, t.TempDir()
	four, err := Explore(r, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(growthOf(one.Growth), growthOf(four.Growth)) {
		t.Fatalf("rounds differ between -j 1 and -j 4:\n%+v\n%+v", one.Growth, four.Growth)
	}
	if one.Corpus != four.Corpus || one.Corpus == 0 {
		t.Fatalf("corpus %d and %d", one.Corpus, four.Corpus)
	}
}

func TestGuestExploreFailureReplaysAndShrinks(t *testing.T) {
	r := feedbackRunner(t, false)
	out, err := Explore(r, ExploreOptions{Lo: 1, Hi: 8, Rounds: 2, Batch: 4, Seed: 1, Jobs: 2, Corpus: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if out.First == nil {
		t.Fatal("the guest without fsync should fail within the baseline")
	}
	s := scheduleRoundTrip(t, out.First.Schedule(Schedule{}))
	replay := r.Replay(out.First.Seed, s)
	if !replay.sameFailure(*out.First) {
		t.Fatalf("replay failed with %q, want %q", replay.Reason, out.First.Reason)
	}
	shrunk := Shrink(r, *out.First, ShrinkOptions{Jobs: 2, MaxRuns: 20})
	if !shrunk.Result.sameFailure(*out.First) {
		t.Fatalf("shrink kept %q, want %q", shrunk.Result.Reason, out.First.Reason)
	}
}

func TestGuestExploreCorpusPersists(t *testing.T) {
	r := feedbackRunner(t, true)
	dir := t.TempDir()
	first, err := Explore(r, ExploreOptions{Lo: 1, Hi: 3, Rounds: 1, Batch: 2, Seed: 1, Jobs: 2, Corpus: dir, KeepGoing: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Corpus == 0 {
		t.Fatal("no entries kept")
	}
	second, err := Explore(r, ExploreOptions{Lo: 1, Hi: 1, Rounds: 1, Batch: 2, Seed: 1, Jobs: 2, Corpus: dir, KeepGoing: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Loaded != first.Corpus {
		t.Fatalf("loaded %d entries, want %d", second.Loaded, first.Corpus)
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if err := os.WriteFile(paths[0], []byte(`{"seed":1,"faults":[{"at":"1s","kind":"nonsense","node":"store"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := Explore(r, ExploreOptions{Lo: 1, Hi: 1, Rounds: 1, Batch: 2, Seed: 1, Jobs: 2, Corpus: dir, KeepGoing: true})
	if err != nil {
		t.Fatal(err)
	}
	if third.Stale != 1 {
		t.Fatalf("stale %d, want 1", third.Stale)
	}
}

func TestGuestSweepUntilNoveltyStopsAndDoesNotDependOnJobs(t *testing.T) {
	r := feedbackRunner(t, true)
	opts := SweepOptions{Batch: 5, UntilNovelty: 2, KeepGoing: true}
	opts.Jobs = 1
	one := Sweep(r, 1, 30, opts)
	opts.Jobs = 4
	four := Sweep(r, 1, 30, opts)
	if !reflect.DeepEqual(growthOf(one.Growth), growthOf(four.Growth)) {
		t.Fatalf("growth differs between -j 1 and -j 4:\n%+v\n%+v", one.Growth, four.Growth)
	}
	if one.Stopped == "" || one.Elapsed <= 0 || one.VirtualTime <= 0 || one.SeedsPerSecond() <= 0 || one.VirtualPerWall() <= 0 {
		t.Fatalf("summary %+v", one)
	}
}

func TestStreamSeedFoldsChainedKeys(t *testing.T) {
	w := &world{seed: 42, branches: []Branch{{Key: 3}, {Key: 3}, {Key: 5, Stream: "disk"}}}
	seeds := map[uint64]bool{}
	for taken := range len(w.branches) + 1 {
		w.taken = taken
		seeds[w.streamSeed("disk")] = true
	}
	if len(seeds) != 4 {
		t.Fatalf("disk stream seeds collide across branches: %v", seeds)
	}
	w.taken = 3
	if w.streamSeed("net") == w.streamSeed("disk") {
		t.Fatal("a stream branch re-keyed another stream")
	}
}

func TestCheckBranches(t *testing.T) {
	cases := []struct {
		name string
		in   []Branch
		bad  int
	}{
		{"ordered", []Branch{{At: 1, Key: 1}, {At: 2, Key: 2, Stream: "sched"}}, 0},
		{"out of order", []Branch{{At: 5, Key: 1}, {At: 2, Key: 2}}, 1},
		{"negative", []Branch{{At: -1, Key: 1}}, 1},
		{"unknown stream", []Branch{{At: 1, Key: 1, Stream: "nope"}}, 1},
	}
	for _, c := range cases {
		if got := checkBranches(c.in); len(got) != c.bad {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestSleb(t *testing.T) {
	cases := []struct {
		in   int64
		want []byte
	}{{0, []byte{0}}, {-1, []byte{0x7f}}, {63, []byte{0x3f}}, {64, []byte{0xc0, 0x00}}}
	for _, c := range cases {
		if got := sleb(c.in); !slices.Equal(got, c.want) {
			t.Errorf("sleb(%d) = %x, want %x", c.in, got, c.want)
		}
	}
}

func TestActiveFaultsFollowsOpenWindows(t *testing.T) {
	w := &world{now: int64(time.Second), faults: []Fault{
		{At: Duration(100 * time.Millisecond), Kind: FaultPartition, Until: Duration(2 * time.Second)},
		{At: Duration(200 * time.Millisecond), Kind: FaultCrash, Until: Duration(500 * time.Millisecond)},
	}, nextFault: 2, ioFaults: []Fault{{At: Duration(300 * time.Millisecond), Kind: FaultPause, Until: Duration(3 * time.Second)}}}
	if got, want := activeNames(w.activeFaults()), "partition,pause"; got != want {
		t.Fatalf("active %q, want %q", got, want)
	}
}

func TestShiftFaultKeepsOpFaultsValidAndWindowsIntact(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	op := Fault{Kind: FaultCrashIO, Op: 1, Until: Duration(time.Second)}
	window := Fault{Kind: FaultPartition, At: Duration(time.Millisecond), Until: Duration(10 * time.Millisecond)}
	for range 200 {
		shiftFault(&op, r, 10*time.Second)
		if op.Op < 1 {
			t.Fatalf("op %d", op.Op)
		}
		before := window.Until - window.At
		shiftFault(&window, r, 10*time.Second)
		if window.Until-window.At != before || window.At < 0 {
			t.Fatalf("window %+v", window)
		}
	}
}
