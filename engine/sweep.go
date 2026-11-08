package engine

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type SweepOptions struct {
	Jobs         int
	KeepGoing    bool
	Batch        int
	UntilNovelty int
	OnResult     func(Result)
	OnRound      func(GrowthPoint)
	Deadline     time.Time
	Progress     func(SweepProgress)
}

type SweepProgress struct {
	Done    int64
	Total   uint64
	Failed  int64
	Elapsed time.Duration
	Virtual time.Duration
}

func (p SweepProgress) SeedsPerSecond() float64 {
	if p.Elapsed <= 0 {
		return 0
	}
	return float64(p.Done) / p.Elapsed.Seconds()
}

func (p SweepProgress) VirtualPerWall() float64 {
	if p.Elapsed <= 0 {
		return 0
	}
	return p.Virtual.Seconds() / p.Elapsed.Seconds()
}

func (p SweepProgress) Remaining() time.Duration {
	rate := p.SeedsPerSecond()
	if rate <= 0 || uint64(p.Done) >= p.Total {
		return 0
	}
	return time.Duration(float64(p.Total-uint64(p.Done)) / rate * float64(time.Second))
}

const progressInterval = time.Second

type sweepMeter struct {
	done, failed, virtual atomic.Int64
	timedOut              atomic.Bool
}

func (m *sweepMeter) run(r *Runner, seed uint64) Result {
	res := r.Run(seed, nil)
	m.done.Add(1)
	m.virtual.Add(int64(res.Duration))
	if res.Failed {
		m.failed.Add(1)
	}
	return res
}

func (m *sweepMeter) expired(deadline time.Time) bool {
	if deadline.IsZero() || time.Now().Before(deadline) {
		return false
	}
	m.timedOut.Store(true)
	return true
}

func (m *sweepMeter) snapshot(total uint64, started time.Time) SweepProgress {
	return SweepProgress{Done: m.done.Load(), Total: total, Failed: m.failed.Load(), Elapsed: time.Since(started), Virtual: time.Duration(m.virtual.Load())}
}

func (m *sweepMeter) report(progress func(SweepProgress), total uint64, started time.Time) func() {
	if progress == nil {
		return func() {}
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				progress(m.snapshot(total, started))
			}
		}
	}()
	return func() {
		close(stop)
		<-stopped
	}
}

type Summary struct {
	First       *Result
	Failures    []Result
	Passed      int
	Unfinished  int
	Coverage    Coverage
	Buggify     BuggifySites
	Elapsed     time.Duration
	VirtualTime time.Duration
	Growth      []GrowthPoint
	Stopped     string

	outcomes map[string]uint64
	speedups []float64
	lo, hi   uint64
}

func (s *Summary) Runs() int { return s.Passed + len(s.Failures) }

func (s *Summary) SeedsPerSecond() float64 {
	if s.Elapsed <= 0 {
		return 0
	}
	return float64(s.Runs()) / s.Elapsed.Seconds()
}

func (s *Summary) VirtualPerWall() float64 {
	if s.Elapsed <= 0 {
		return 0
	}
	return s.VirtualTime.Seconds() / s.Elapsed.Seconds()
}

func (s *Summary) record(res Result) {
	s.Coverage.add(res.Properties)
	s.Buggify.add(res.Buggify)
	s.VirtualTime += res.Duration
	s.recordUsage(res)
	if !res.Failed {
		s.Passed++
		if !res.Finished {
			s.Unfinished++
		}
		return
	}
	if s.First == nil || res.Seed < s.First.Seed {
		first := res
		s.First = &first
	}
	brief := res
	brief.Trace, brief.Properties, brief.Buggify, brief.Signals, brief.Functions = nil, nil, nil, nil, nil
	s.Failures = append(s.Failures, brief)
}

func newSummary(r *Runner) Summary {
	return Summary{Coverage: newCoverage(r.catalog), Buggify: newBuggifySites(r.cfg, r.catalog)}
}

func newSweepSummary(r *Runner) Summary {
	s := newSummary(r)
	s.outcomes = map[string]uint64{}
	return s
}

func Sweep(r *Runner, lo, hi uint64, opts SweepOptions) Summary {
	started := time.Now()
	var (
		sum Summary
		m   sweepMeter
	)
	total := hi - lo + 1
	stop := m.report(opts.Progress, total, started)
	defer stop()
	if opts.UntilNovelty > 0 {
		sum = sweepUntilNovelty(r, lo, hi, opts, started, &m)
	} else {
		sum = sweepAll(r, lo, hi, opts, &m)
	}
	if m.timedOut.Load() {
		sum.Stopped = fmt.Sprintf("time limit reached after %d of %d seeds", sum.Runs(), total)
	}
	sum.Elapsed = time.Since(started)
	return sum
}

func sweepAll(r *Runner, lo, hi uint64, opts SweepOptions, m *sweepMeter) Summary {
	var (
		next atomic.Uint64
		mu   sync.Mutex
		sum  = newSweepSummary(r)
		wg   sync.WaitGroup
	)
	next.Store(lo)
	for range max(opts.Jobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				seed := next.Add(1) - 1
				mu.Lock()
				stop := seed > hi || (!opts.KeepGoing && sum.First != nil && seed > sum.First.Seed)
				mu.Unlock()
				if stop || m.expired(opts.Deadline) {
					return
				}
				res := m.run(r, seed)
				mu.Lock()
				sum.record(res)
				if opts.OnResult != nil {
					opts.OnResult(res)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return sum
}

func sweepUntilNovelty(r *Runner, lo, hi uint64, opts SweepOptions, started time.Time, m *sweepMeter) Summary {
	sum, fb, quiet := newSweepSummary(r), newFeedback(), 0
	batch := uint64(max(opts.Batch, 1))
	for round, seed := 1, lo; seed <= hi && !m.expired(opts.Deadline); round++ {
		n := min(batch, hi-seed+1)
		base := seed
		results := make([]Result, n)
		ran := make([]bool, n)
		forEach(int(n), opts.Jobs, func(i int) {
			if !m.expired(opts.Deadline) {
				results[i], ran[i] = m.run(r, base+uint64(i)), true
			}
		})
		seed += n
		var fresh []string
		functions := 0
		for i, res := range results {
			if !ran[i] {
				continue
			}
			sum.record(res)
			if opts.OnResult != nil {
				opts.OnResult(res)
			}
			sigs, funcs := fb.absorb(res)
			fresh, functions = append(fresh, sigs...), functions+funcs
		}
		point := fb.point(round, sum.Runs(), 0, len(sum.Failures), fresh, functions, time.Since(started))
		sum.Growth = append(sum.Growth, point)
		if opts.OnRound != nil {
			opts.OnRound(point)
		}
		if m.timedOut.Load() {
			return sum
		}
		if !opts.KeepGoing && sum.First != nil {
			sum.Stopped = "failure found"
			return sum
		}
		if quiet = quietRounds(point, quiet); quiet >= opts.UntilNovelty {
			sum.Stopped = fmt.Sprintf("%d batches in a row added nothing new", quiet)
			return sum
		}
	}
	if !m.timedOut.Load() {
		sum.Stopped = "seed range finished"
	}
	return sum
}
