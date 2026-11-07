package engine

import "fmt"

type Verdict string

const (
	VerdictBug              Verdict = "bug"
	VerdictNondeterministic Verdict = "nondeterministic"
	VerdictTimeout          Verdict = "timeout"
)

type Classification struct {
	Verdict    Verdict `json:"class"`
	Divergence string  `json:"divergence,omitempty"`
}

func (c Classification) String() string {
	if c.Divergence == "" {
		return string(c.Verdict)
	}
	return fmt.Sprintf("%s (replay diverges at %s)", c.Verdict, c.Divergence)
}

func (r *Runner) Classify(failed Result, faults []Fault) Classification {
	switch failed.Failure.Class {
	case FailWatchdog:
		return Classification{Verdict: VerdictTimeout}
	case FailNondeterminism:
		return Classification{Verdict: VerdictNondeterministic}
	}
	replay := r.once(failed.Seed, faults)
	if diff, diverged := divergence(failed.Trace, replay.Trace); diverged {
		return Classification{Verdict: VerdictNondeterministic, Divergence: diff}
	}
	if replay.Reason != failed.Reason {
		return Classification{Verdict: VerdictNondeterministic, Divergence: fmt.Sprintf("outcome: %q vs %q", failed.Reason, replay.Reason)}
	}
	return Classification{Verdict: VerdictBug}
}
