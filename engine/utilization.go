package engine

import (
	"fmt"
	"slices"
)

const (
	utilizationBatches = 10
	batchesToCompare   = 2
)

type UtilizationBatch struct {
	From uint64 `json:"from_seed"`
	To   uint64 `json:"to_seed"`
	New  int    `json:"new_outcomes"`
}

type Utilization struct {
	Outcomes      int                `json:"property_outcomes"`
	Batches       []UtilizationBatch `json:"batches"`
	SpeedupMin    float64            `json:"speedup_min"`
	SpeedupMedian float64            `json:"speedup_median"`
	SpeedupMax    float64            `json:"speedup_max"`
	Advice        string             `json:"advice"`
}

func (s *Summary) recordUsage(res Result) {
	if s.outcomes == nil {
		return
	}
	if s.Runs() == 0 || res.Seed < s.lo {
		s.lo = res.Seed
	}
	s.hi = max(s.hi, res.Seed)
	if res.Elapsed > 0 {
		s.speedups = append(s.speedups, res.Duration.Seconds()/res.Elapsed.Seconds())
	}
	for msg, p := range res.Properties {
		if p.Hits > 0 {
			s.sawOutcome(msg+" reached", res.Seed)
		}
		if p.True > 0 {
			s.sawOutcome(msg+" true", res.Seed)
		}
		if p.Hits > p.True {
			s.sawOutcome(msg+" false", res.Seed)
		}
	}
}

func (s *Summary) sawOutcome(key string, seed uint64) {
	if first, ok := s.outcomes[key]; !ok || seed < first {
		s.outcomes[key] = seed
	}
}

func (s *Summary) Utilization() *Utilization {
	if s.outcomes == nil || s.Runs() == 0 {
		return nil
	}
	span := s.hi - s.lo + 1
	count := min(span, utilizationBatches)
	size := span / count
	u := &Utilization{Outcomes: len(s.outcomes), Batches: make([]UtilizationBatch, count)}
	for i := range u.Batches {
		from := s.lo + uint64(i)*size
		u.Batches[i] = UtilizationBatch{From: from, To: from + size - 1}
	}
	u.Batches[count-1].To = s.hi
	for _, seed := range s.outcomes {
		u.Batches[min((seed-s.lo)/size, count-1)].New++
	}
	if len(s.speedups) > 0 {
		sorted := slices.Sorted(slices.Values(s.speedups))
		u.SpeedupMin, u.SpeedupMedian, u.SpeedupMax = sorted[0], sorted[len(sorted)/2], sorted[len(sorted)-1]
	}
	u.Advice = u.advice()
	return u
}

func (u *Utilization) advice() string {
	if u.Outcomes == 0 {
		return "No sim.Sometimes, sim.Always or sim.Assert property was reached, so nothing here says whether more seeds would help."
	}
	if len(u.Batches) < batchesToCompare {
		return "Too few seeds to compare batches; run a wider range to see whether new property outcomes are still appearing."
	}
	quiet := 0
	for quiet < len(u.Batches) && u.Batches[len(u.Batches)-1-quiet].New == 0 {
		quiet++
	}
	last := u.Batches[len(u.Batches)-1]
	if quiet == 0 {
		return fmt.Sprintf("The last batch (seeds %d-%d) still reached %d new property outcomes, so more seeds are likely to find more.", last.From, last.To, last.New)
	}
	return fmt.Sprintf("No new property outcome in the last %d of %d batches (seeds %d-%d), so more seeds of this config are unlikely to add coverage; change the faults or workload, or try seedfall explore.", quiet, len(u.Batches), u.Batches[len(u.Batches)-quiet].From, last.To)
}
