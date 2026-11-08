package engine

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func usageResult(seed uint64, msgs ...string) Result {
	props := properties{}
	for _, msg := range msgs {
		props[msg] = &Property{Hits: 2, True: 1}
	}
	return Result{Seed: seed, Properties: props, Duration: 4 * time.Second, Elapsed: time.Second}
}

func usageSummary(results []Result) Summary {
	sum := Summary{Coverage: Coverage{}, Buggify: BuggifySites{}, outcomes: map[string]uint64{}}
	for _, res := range results {
		res.Failed = false
		sum.record(res)
	}
	return sum
}

func TestUtilizationIsIndependentOfCompletionOrder(t *testing.T) {
	var forward []Result
	for seed := uint64(1); seed <= 20; seed++ {
		forward = append(forward, usageResult(seed, "a"))
	}
	forward[14] = usageResult(15, "a", "b")
	backward := make([]Result, 0, len(forward))
	for i := len(forward) - 1; i >= 0; i-- {
		backward = append(backward, forward[i])
	}
	f, b := usageSummary(forward), usageSummary(backward)
	got, other := f.Utilization(), b.Utilization()
	if got.Advice != other.Advice || len(got.Batches) != len(other.Batches) {
		t.Fatalf("%+v vs %+v", got, other)
	}
	for i := range got.Batches {
		if got.Batches[i] != other.Batches[i] {
			t.Errorf("batch %d: %+v vs %+v", i, got.Batches[i], other.Batches[i])
		}
	}
	if got.Outcomes != 6 || got.Batches[0].New != 3 || got.Batches[7].New != 3 {
		t.Fatalf("outcomes %d, batches %+v", got.Outcomes, got.Batches)
	}
	if got.SpeedupMedian != 4 || got.SpeedupMin != 4 || got.SpeedupMax != 4 {
		t.Fatalf("speedups %+v", got)
	}
}

func TestUtilizationAdvice(t *testing.T) {
	quiet := []Result{}
	for seed := uint64(1); seed <= 20; seed++ {
		quiet = append(quiet, usageResult(seed, "a"))
	}
	late := append([]Result{}, quiet...)
	late[19] = usageResult(20, "a", "late")
	for name, c := range map[string]struct {
		results []Result
		want    string
	}{
		"quiet":   {quiet, "unlikely to add coverage"},
		"late":    {late, "likely to find more"},
		"no data": {[]Result{usageResult(1)}, "No sim.Sometimes"},
		"one":     {[]Result{usageResult(1, "a")}, "Too few seeds"},
	} {
		s := usageSummary(c.results)
		if got := s.Utilization().Advice; !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestExploreSummaryHasNoUtilization(t *testing.T) {
	s := Summary{Coverage: Coverage{}, Buggify: BuggifySites{}}
	s.record(usageResult(1, "a"))
	if s.Utilization() != nil {
		t.Fatal("a summary without seed tracking reported utilization")
	}
}

func TestSweepStopsAtDeadline(t *testing.T) {
	r := guestRunner(t, false)
	past := time.Now().Add(-time.Second)
	for name, opts := range map[string]SweepOptions{
		"plain":         {Jobs: 2, KeepGoing: true, Deadline: past},
		"until novelty": {Jobs: 2, KeepGoing: true, Deadline: past, UntilNovelty: 3, Batch: 4},
	} {
		sum := Sweep(r, 1, 8, opts)
		if sum.Runs() != 0 || sum.Stopped != "time limit reached after 0 of 8 seeds" {
			t.Errorf("%s: %d runs, stopped %q", name, sum.Runs(), sum.Stopped)
		}
	}
	finished := Sweep(r, 1, 2, SweepOptions{Jobs: 2, KeepGoing: true, Deadline: time.Now().Add(time.Hour)})
	if finished.Stopped != "" || finished.Runs() != 2 {
		t.Errorf("a sweep inside its deadline: %d runs, stopped %q", finished.Runs(), finished.Stopped)
	}
}

func TestSweepMeterTicksAndStops(t *testing.T) {
	var m sweepMeter
	m.done.Store(3)
	m.failed.Store(1)
	m.virtual.Store(int64(8 * time.Second))
	var ticks atomic.Int64
	seen := make(chan SweepProgress, 1)
	stop := m.report(func(p SweepProgress) {
		if ticks.Add(1) == 1 {
			seen <- p
		}
	}, 12, time.Now().Add(-2*time.Second))
	select {
	case p := <-seen:
		if p.Done != 3 || p.Total != 12 || p.Failed != 1 || p.Virtual != 8*time.Second {
			t.Fatalf("snapshot %+v", p)
		}
		if p.SeedsPerSecond() <= 0 || p.Remaining() <= 0 || p.VirtualPerWall() <= 0 {
			t.Fatalf("rates of %+v", p)
		}
	case <-time.After(10 * progressInterval):
		t.Fatal("no progress tick")
	}
	stop()
	after := ticks.Load()
	time.Sleep(2 * progressInterval)
	if ticks.Load() != after {
		t.Fatal("progress kept ticking after stop")
	}
}

func TestVersionPrefersLinkerValues(t *testing.T) {
	buildVersion, buildCommit, buildDate = "v9.9.9", "abc", "2026-11-08"
	defer func() { buildVersion, buildCommit, buildDate = "", "", "" }()
	got := Binary()
	if got.Version != "v9.9.9" || Version() != "v9.9.9" || got.Commit != "abc" || got.Date != "2026-11-08" || got.GoVersion == "" || got.Platform == "" {
		t.Fatalf("%+v", got)
	}
}
