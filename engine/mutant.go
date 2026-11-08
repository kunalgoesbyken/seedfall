package engine

type MutantOutcome string

const (
	MutantKilled      MutantOutcome = "killed"
	MutantSurvived    MutantOutcome = "survived"
	MutantUnexercised MutantOutcome = "not exercised"
)

type MutantResult struct {
	Mutation Mutation
	KilledBy *Result
	Passed   int
	Weakened int64
}

func (m MutantResult) Outcome() MutantOutcome {
	switch {
	case m.KilledBy != nil:
		return MutantKilled
	case m.Weakened == 0:
		return MutantUnexercised
	}
	return MutantSurvived
}

func SweepMutants(r *Runner, mutations []Mutation, lo, hi uint64, opts SweepOptions) []MutantResult {
	out := make([]MutantResult, 0, len(mutations))
	for _, m := range mutations {
		out = append(out, sweepMutant(r, m, lo, hi, opts))
	}
	return out
}

func sweepMutant(r *Runner, m Mutation, lo, hi uint64, opts SweepOptions) MutantResult {
	r.Mutation = m
	defer func() { r.Mutation = NoMutation }()
	before := r.Weakened()
	sum := Sweep(r, lo, hi, opts)
	return MutantResult{Mutation: m, KilledBy: sum.First, Passed: sum.Passed, Weakened: r.Weakened() - before}
}
