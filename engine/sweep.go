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

func Sweep(r *Runner, lo, hi uint64, opts SweepOptions) Summary {
	started := time.Now()
	var sum Summary
	if opts.UntilNovelty > 0 {
		sum = sweepUntilNovelty(r, lo, hi, opts, started)
	} else {
		sum = sweepAll(r, lo, hi, opts)
	}
	sum.Elapsed = time.Since(started)
	return sum
}

func sweepAll(r *Runner, lo, hi uint64, opts SweepOptions) Summary {
	var (
		next atomic.Uint64
		mu   sync.Mutex
		sum  = newSummary(r)
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
				if stop {
					return
				}
				res := r.Run(seed, nil)
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

func sweepUntilNovelty(r *Runner, lo, hi uint64, opts SweepOptions, started time.Time) Summary {
	sum, fb, quiet := newSummary(r), newFeedback(), 0
	batch := uint64(max(opts.Batch, 1))
	for round, seed := 1, lo; seed <= hi; round++ {
		n := min(batch, hi-seed+1)
		base := seed
		results := make([]Result, n)
		forEach(int(n), opts.Jobs, func(i int) { results[i] = r.Run(base+uint64(i), nil) })
		seed += n
		var fresh []string
		functions := 0
		for _, res := range results {
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
		if quiet = quietRounds(point, quiet); quiet >= opts.UntilNovelty {
			sum.Stopped = fmt.Sprintf("%d batches in a row added nothing new", quiet)
			return sum
		}
		if !opts.KeepGoing && sum.First != nil {
			sum.Stopped = "failure found"
			return sum
		}
	}
	sum.Stopped = "seed range finished"
	return sum
}
