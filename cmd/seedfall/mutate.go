package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/kunalgoesbyken/seedfall/engine"
)

type mutantResult struct {
	mutation engine.Mutation
	killedBy *engine.Result
	passed   int
	weakened int64
}

func (m mutantResult) verdict() string {
	switch {
	case m.killedBy != nil:
		return fmt.Sprintf("killed     seed %d: %s", m.killedBy.Seed, m.killedBy.Reason)
	case m.weakened == 0:
		return "not exercised (the operation never ran, or has no effect in this configuration)"
	}
	return fmt.Sprintf("SURVIVED   %d seeds passed although the operation was weakened %d times", m.passed, m.weakened)
}

func cmdMutate(args []string) error {
	fs := flag.NewFlagSet("mutate", flag.ExitOnError)
	conf := fs.String("c", "seedfall.json", "config file")
	seeds := fs.String("seeds", "1-100", "seed or seed range for each mutant")
	jobs := fs.Int("j", runtime.NumCPU(), "parallel runs")
	fs.Parse(args)
	lo, hi, err := engine.ParseSeeds(*seeds)
	if err != nil {
		return err
	}
	r, _, err := load(*conf)
	if err != nil {
		return err
	}
	opts := engine.SweepOptions{Jobs: *jobs}
	if base := engine.Sweep(r, lo, hi, opts); base.First != nil {
		return fmt.Errorf("the unmutated system already fails on seed %d (%s); fix that first", base.First.Seed, base.First.Reason)
	}
	fmt.Printf("baseline: seeds %s pass\n\n", *seeds)
	survived := 0
	for _, m := range engine.Mutations {
		res := runMutant(r, m, lo, hi, opts)
		fmt.Printf("  %-14s %s\n  %-14s %s\n\n", m, res.verdict(), "", m.Describe())
		if res.killedBy == nil && res.weakened > 0 {
			survived++
		}
	}
	if survived > 0 {
		fmt.Printf("%d mutants survived: your workload does not notice those durability guarantees disappearing\n", survived)
		os.Exit(1)
	}
	fmt.Println("every exercised mutant was killed")
	return nil
}

func runMutant(r *engine.Runner, m engine.Mutation, lo, hi uint64, opts engine.SweepOptions) mutantResult {
	r.Mutation = m
	defer func() { r.Mutation = engine.NoMutation }()
	before := r.Weakened()
	sum := engine.Sweep(r, lo, hi, opts)
	return mutantResult{mutation: m, killedBy: sum.First, passed: sum.Passed, weakened: r.Weakened() - before}
}
