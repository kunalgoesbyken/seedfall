package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

type Replayer interface {
	Replay(seed uint64, s Schedule) Result
}

type ShrinkOptions struct {
	Jobs     int
	MaxRuns  int
	MaxTime  time.Duration
	Progress func(ShrinkStep)
}

type ShrinkStep struct {
	Runs   int
	Faults int
	Change string
}

type ScheduleSize struct {
	Faults   int      `json:"faults"`
	Duration Duration `json:"duration"`
	Ops      int64    `json:"ops,omitempty"`
}

func (s ScheduleSize) String() string {
	out := fmt.Sprintf("%d faults, %s", s.Faults, time.Duration(s.Duration))
	if s.Ops > 0 {
		out += fmt.Sprintf(", %d workload ops", s.Ops)
	}
	return out
}

type Shrunk struct {
	Result    Result
	Schedule  Schedule
	Before    ScheduleSize
	After     ScheduleSize
	Runs      int
	Exhausted bool
}

const shrinkUnit = int64(time.Millisecond)

type shrinker struct {
	r         Replayer
	failing   Result
	opts      ShrinkOptions
	deadline  time.Time
	best      Result
	sched     Schedule
	runs      int
	exhausted bool
}

func Shrink(r Replayer, failing Result, opts ShrinkOptions) Shrunk {
	start := Schedule{Faults: slices.Clone(failing.Faults), Duration: Duration(failing.Horizon), Branches: failing.Branches}
	if start.Faults == nil {
		start.Faults = []Fault{}
	}
	s := &shrinker{r: r, failing: failing, opts: opts, best: failing, sched: start}
	if opts.MaxTime > 0 {
		s.deadline = time.Now().Add(opts.MaxTime)
	}
	s.removeFaults()
	for s.reduceValues() {
		s.removeFaults()
	}
	return Shrunk{Result: s.best, Schedule: s.sched, Before: sizeOf(start, failing), After: sizeOf(s.sched, s.best), Runs: s.runs, Exhausted: s.exhausted}
}

func sizeOf(s Schedule, res Result) ScheduleSize {
	ops := res.Ops
	if s.Ops > 0 {
		ops = min(ops, s.Ops)
	}
	return ScheduleSize{Faults: len(s.Faults), Duration: s.Duration, Ops: ops}
}

func (s *shrinker) spent() bool {
	if (s.opts.MaxRuns > 0 && s.runs >= s.opts.MaxRuns) || (!s.deadline.IsZero() && !time.Now().Before(s.deadline)) {
		s.exhausted = true
	}
	return s.exhausted
}

func (s *shrinker) first(candidates []Schedule) (int, bool) {
	for start := 0; start < len(candidates); {
		if s.spent() {
			return 0, false
		}
		n := min(max(s.opts.Jobs, 1), len(candidates)-start)
		if s.opts.MaxRuns > 0 {
			n = min(n, s.opts.MaxRuns-s.runs)
		}
		results := make([]Result, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() { results[i] = s.r.Replay(s.failing.Seed, candidates[start+i]) })
		}
		wg.Wait()
		for i, res := range results {
			s.runs++
			if res.sameFailure(s.failing) {
				s.best, s.sched = res, candidates[start+i]
				s.sched.Faults = res.Faults
				return start + i, true
			}
		}
		start += n
	}
	return 0, false
}

func (s *shrinker) try(c Schedule) bool {
	_, ok := s.first([]Schedule{c})
	return ok
}

func (s *shrinker) report(format string, args ...any) {
	if s.opts.Progress != nil {
		s.opts.Progress(ShrinkStep{Runs: s.runs, Faults: len(s.sched.Faults), Change: fmt.Sprintf(format, args...)})
	}
}

func (s *shrinker) withFaults(faults []Fault) Schedule {
	c := s.sched
	c.Faults = faults
	return c
}

func (s *shrinker) removeFaults() {
	for n := 2; len(s.sched.Faults) > 0 && !s.exhausted; {
		before := len(s.sched.Faults)
		n = min(n, before)
		chunks := split(s.sched.Faults, n)
		candidates := []Schedule{s.withFaults([]Fault{})}
		if n > 1 {
			candidates = candidates[:0]
			for _, c := range chunks {
				candidates = append(candidates, s.withFaults(c))
			}
		}
		if n > 2 {
			for i := range chunks {
				candidates = append(candidates, s.withFaults(slices.Concat(slices.Delete(slices.Clone(chunks), i, i+1)...)))
			}
		}
		if i, ok := s.first(candidates); ok {
			n = max(n-1, 2)
			if i < len(chunks) {
				n = 2
			}
			s.report("removed %d of %d faults", before-len(s.sched.Faults), before)
			continue
		}
		if n >= before {
			break
		}
		n = min(2*n, before)
	}
}

func split(faults []Fault, n int) [][]Fault {
	out := make([][]Fault, 0, n)
	for i := range n {
		out = append(out, slices.Clone(faults[i*len(faults)/n:(i+1)*len(faults)/n]))
	}
	return out
}

func bisect(lo, hi, unit int64, ok func(int64) bool) bool {
	if lo >= hi {
		return false
	}
	if ok(lo) {
		return true
	}
	found := false
	for (hi-lo)/2 >= unit {
		mid := lo + (hi-lo)/2/unit*unit
		if ok(mid) {
			hi, found = mid, true
		} else {
			lo = mid
		}
	}
	return found
}

func (s *shrinker) reduceValues() bool {
	changed := s.reduceOps()
	changed = s.reduceDuration() || changed
	for i := 0; i < len(s.sched.Faults) && !s.exhausted; i++ {
		changed = s.reduceFault(s.sched.Faults[i]) || changed
	}
	return changed
}

func (s *shrinker) reduceOps() bool {
	wanted := s.best.Ops
	hi := wanted
	if s.sched.Ops > 0 {
		hi = min(hi, s.sched.Ops)
	}
	if !bisect(1, hi, 1, func(v int64) bool {
		c := s.sched
		c.Ops = v
		return s.try(c)
	}) {
		return false
	}
	s.report("sim.Budget capped at %d of %d operations", s.sched.Ops, wanted)
	return true
}

func (s *shrinker) reduceDuration() bool {
	before := s.sched.Duration
	if !bisect(shrinkUnit, int64(before), shrinkUnit, func(v int64) bool {
		c := s.sched
		c.Duration = Duration(v)
		return s.try(c)
	}) {
		return false
	}
	s.report("duration %s instead of %s", time.Duration(s.sched.Duration), time.Duration(before))
	return true
}

func (s *shrinker) reduceFault(f Fault) bool {
	changed := false
	edit := func(what string, lo, hi, unit int64, set func(Fault, int64) Fault) {
		if next, ok := s.bisectFault(f, lo, hi, unit, set); ok {
			f, changed = next, true
			s.report("%s: %s", what, strings.TrimSpace(f.Describe()))
		}
	}
	if f.Until > f.At {
		edit("shorter", int64(f.At), int64(f.Until), shrinkUnit, func(g Fault, v int64) Fault {
			g.Until = Duration(v)
			return g
		})
	}
	if f.Shift != 0 {
		sign := Duration(1)
		if f.Shift < 0 {
			sign = -1
		}
		edit("smaller jump", 0, int64(f.Shift*sign), shrinkUnit, func(g Fault, v int64) Fault {
			g.Shift = sign * Duration(v)
			return g
		})
	}
	for len(f.Side) > 1 {
		next, ok := s.narrowSide(f)
		if !ok {
			break
		}
		f, changed = next, true
		s.report("narrower: %s", strings.TrimSpace(f.Describe()))
	}
	if !observedAtRuntime(f.Kind) {
		edit("earlier", 0, int64(f.At), shrinkUnit, func(g Fault, v int64) Fault {
			if g.Until >= g.At {
				g.Until += Duration(v) - g.At
			}
			g.At = Duration(v)
			return g
		})
	}
	return changed
}

func (s *shrinker) replacing(old, next Fault) (Schedule, bool) {
	i := slices.IndexFunc(s.sched.Faults, func(g Fault) bool { return reflect.DeepEqual(g, old) })
	if i < 0 {
		return Schedule{}, false
	}
	faults := slices.Clone(s.sched.Faults)
	faults[i] = next
	slices.SortStableFunc(faults, func(a, b Fault) int { return int(a.At - b.At) })
	return s.withFaults(faults), true
}

func (s *shrinker) bisectFault(f Fault, lo, hi, unit int64, set func(Fault, int64) Fault) (Fault, bool) {
	cur := f
	ok := bisect(lo, hi, unit, func(v int64) bool {
		next := set(f, v)
		c, found := s.replacing(cur, next)
		if !found || !s.try(c) {
			return false
		}
		cur = next
		return true
	})
	return cur, ok
}

func (s *shrinker) narrowSide(f Fault) (Fault, bool) {
	var nexts []Fault
	var candidates []Schedule
	for i := range f.Side {
		next := f
		next.Side = slices.Delete(slices.Clone(f.Side), i, i+1)
		c, found := s.replacing(f, next)
		if !found {
			return f, false
		}
		nexts, candidates = append(nexts, next), append(candidates, c)
	}
	i, ok := s.first(candidates)
	if !ok {
		return f, false
	}
	return nexts[i], true
}
