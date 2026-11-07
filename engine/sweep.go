package engine

import (
	"sync"
	"sync/atomic"
)

type SweepOptions struct {
	Jobs      int
	KeepGoing bool
	OnResult  func(Result)
}

type Summary struct {
	First      *Result
	Failures   []Result
	Passed     int
	Unfinished int
	Coverage   Coverage
}

func (s *Summary) record(res Result) {
	s.Coverage.add(res.Properties)
	if !res.Failed {
		s.Passed++
		if !res.Finished {
			s.Unfinished++
		}
		return
	}
	if s.First == nil || res.Seed < s.First.Seed {
		s.First = &res
	}
	brief := res
	brief.Trace, brief.Properties = nil, nil
	s.Failures = append(s.Failures, brief)
}

func Sweep(r *Runner, lo, hi uint64, opts SweepOptions) Summary {
	var (
		next atomic.Uint64
		mu   sync.Mutex
		sum  = Summary{Coverage: newCoverage(r.catalog)}
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
