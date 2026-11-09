package main

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const (
	barWidth        = 30
	defaultPoints   = 16
	defaultBranches = 20
)

func cmdCause(args []string) error {
	fs := newFlags("cause")
	ca := configFlags(fs)
	seed := seedFlag(fs)
	points := fs.Int("points", defaultPoints, "evenly spaced moments to branch from, in addition to every fault")
	branches := fs.Int("branches", defaultBranches, "alternate futures run from each moment")
	jobs := fs.Int("j", runtime.NumCPU(), "parallel runs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkJobs(*jobs); err != nil {
		return err
	}
	if err := nonNegative(namedInt{"-points", *points}, namedInt{"-branches", *branches}); err != nil {
		return err
	}
	r, _, err := load(ca.ref())
	if err != nil {
		return err
	}
	started := time.Now()
	original, curve, err := engine.Cause(r, *seed, engine.CauseOptions{Points: *points, Branches: *branches, Jobs: *jobs})
	if err != nil {
		return err
	}
	fmt.Printf("seed %d fails at t=%s: %s\n\n", *seed, original.At, original.Reason)
	fmt.Printf("chance of failing when everything after a moment is re-randomized (%d futures each):\n\n", *branches)
	for _, p := range curve {
		fmt.Printf("  %9s  %4.0f%%  %-*s%s\n", p.At.Round(time.Microsecond), p.Probability()*100, barWidth, bar(p.Probability()), faultNote(p.Faults))
	}
	if from, to, ok := biggestJump(curve); ok {
		fmt.Printf("\nbiggest jump: %.0f%% -> %.0f%% at %s%s\n", from.Probability()*100, to.Probability()*100, to.At.Round(time.Microsecond), faultNote(to.Faults))
	}
	fmt.Printf("\n%d runs in %s\n", len(curve)**branches, time.Since(started).Round(time.Millisecond))
	return nil
}

func bar(p float64) string { return strings.Repeat("█", int(p*barWidth+0.5)) }

func faultNote(faults []engine.Fault) string {
	if len(faults) == 0 {
		return ""
	}
	notes := make([]string, len(faults))
	for i, f := range faults {
		notes[i] = strings.TrimSpace(f.Describe())
	}
	return "  <- after " + strings.Join(notes, "; ")
}

func biggestJump(curve []engine.CausePoint) (engine.CausePoint, engine.CausePoint, bool) {
	best, at := 0.0, -1
	for i := 1; i < len(curve); i++ {
		if d := curve[i].Probability() - curve[i-1].Probability(); d > best {
			best, at = d, i
		}
	}
	if at < 0 {
		return engine.CausePoint{}, engine.CausePoint{}, false
	}
	return curve[at-1], curve[at], true
}
