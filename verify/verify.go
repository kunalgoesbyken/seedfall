package verify

import (
	"cmp"
	"fmt"
	"sync"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
)

type Target struct {
	Label    string
	Seed     uint64
	Schedule engine.Schedule
	Rate     float64
}

type Plan struct {
	Targets    []Target
	Lo, Hi     uint64
	Rate       float64
	CheckSeeds int
	Mutate     bool
	Jobs       int
}

type Failure struct {
	Seed           uint64                `json:"seed"`
	Reason         string                `json:"reason"`
	Classification engine.Classification `json:"classification"`
}

type Replay struct {
	Target  string   `json:"target"`
	Seed    uint64   `json:"seed"`
	Failure *Failure `json:"failure,omitempty"`
}

type Sweep struct {
	Seeds      string   `json:"seeds"`
	Count      int      `json:"count"`
	Passed     int      `json:"passed"`
	Rate       float64  `json:"historical_rate,omitempty"`
	Detectable float64  `json:"detectable_rate"`
	Failure    *Failure `json:"failure,omitempty"`
}

type Mutant struct {
	Mutation engine.Mutation      `json:"mutation"`
	Outcome  engine.MutantOutcome `json:"outcome"`
	Seed     uint64               `json:"killed_by_seed,omitempty"`
	Reason   string               `json:"reason,omitempty"`
}

type Proof struct {
	Config      string   `json:"config"`
	Command     string   `json:"command"`
	Passed      bool     `json:"passed"`
	Replays     []Replay `json:"replays"`
	Sweep       *Sweep   `json:"sweep,omitempty"`
	Determinism *Sweep   `json:"determinism,omitempty"`
	Mutants     []Mutant `json:"mutants,omitempty"`
}

func SeedsNeeded(targets []Target) (int, float64) {
	var rate float64
	for _, t := range targets {
		if t.Rate > 0 && (rate == 0 || t.Rate < rate) {
			rate = t.Rate
		}
	}
	return report.SeedsToConfirm(cmp.Or(rate, report.RareRate)), rate
}

func Run(r *engine.Runner, p Plan) Proof {
	proof := Proof{Replays: replay(r, p.Targets, p.Jobs)}
	if failed(proof.Replays) {
		return proof
	}
	opts := engine.SweepOptions{Jobs: p.Jobs}
	proof.Sweep = sweep(r, p.Lo, p.Hi, opts)
	proof.Sweep.Rate = p.Rate
	if proof.Sweep.Failure != nil {
		return proof
	}
	proof.Determinism = doublecheck(r, p.Lo, min(p.Hi, p.Lo+uint64(max(p.CheckSeeds, 1))-1), opts)
	if proof.Determinism.Failure == nil && p.Mutate {
		proof.Mutants = mutants(r, p.Lo, p.Hi, opts)
	}
	proof.Passed = proof.Determinism.Failure == nil && !survived(proof.Mutants)
	return proof
}

func replay(r *engine.Runner, targets []Target, jobs int) []Replay {
	r.Doublecheck = true
	defer func() { r.Doublecheck = false }()
	out := make([]Replay, len(targets))
	slots := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, t := range targets {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			s := t.Schedule
			s.Duration = 0
			res := r.Replay(t.Seed, s)
			out[i] = Replay{Target: t.Label, Seed: t.Seed, Failure: failure(r, res, s)}
		})
	}
	wg.Wait()
	return out
}

func sweep(r *engine.Runner, lo, hi uint64, opts engine.SweepOptions) *Sweep {
	sum := engine.Sweep(r, lo, hi, opts)
	out := &Sweep{Seeds: seedRange(lo, hi), Count: int(hi - lo + 1), Passed: sum.Passed}
	out.Detectable = report.DetectableRate(out.Count)
	if sum.First != nil {
		out.Failure = failure(r, *sum.First, engine.Schedule{})
	}
	return out
}

func doublecheck(r *engine.Runner, lo, hi uint64, opts engine.SweepOptions) *Sweep {
	r.Doublecheck = true
	defer func() { r.Doublecheck = false }()
	return sweep(r, lo, hi, opts)
}

func mutants(r *engine.Runner, lo, hi uint64, opts engine.SweepOptions) []Mutant {
	var out []Mutant
	for _, m := range engine.SweepMutants(r, lo, hi, opts) {
		v := Mutant{Mutation: m.Mutation, Outcome: m.Outcome()}
		if m.KilledBy != nil {
			v.Seed, v.Reason = m.KilledBy.Seed, m.KilledBy.Reason
		}
		out = append(out, v)
	}
	return out
}

func failure(r *engine.Runner, res engine.Result, s engine.Schedule) *Failure {
	if !res.Failed {
		return nil
	}
	return &Failure{Seed: res.Seed, Reason: res.Reason, Classification: r.Classify(res, s)}
}

func failed(replays []Replay) bool {
	for _, rp := range replays {
		if rp.Failure != nil {
			return true
		}
	}
	return false
}

func survived(ms []Mutant) bool {
	for _, m := range ms {
		if m.Outcome == engine.MutantSurvived {
			return true
		}
	}
	return false
}

func seedRange(lo, hi uint64) string {
	if lo == hi {
		return fmt.Sprint(lo)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}
