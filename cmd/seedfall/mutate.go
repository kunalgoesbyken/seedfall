package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/kunalgoesbyken/seedfall/engine"
)

func mutantVerdict(m engine.MutantResult) string {
	switch m.Outcome() {
	case engine.MutantKilled:
		return fmt.Sprintf("killed     seed %d: %s", m.KilledBy.Seed, m.KilledBy.Reason)
	case engine.MutantUnexercised:
		return "not exercised (the operation never ran, or has no effect in this configuration)"
	}
	return fmt.Sprintf("SURVIVED   %d seeds passed although the operation was weakened %d times", m.Passed, m.Weakened)
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
	mutants := engine.SweepMutants(r, lo, hi, opts)
	printMutants(mutants)
	if survived := countSurvivors(mutants); survived > 0 {
		fmt.Printf("%d mutants survived: your workload does not notice those durability guarantees disappearing\n", survived)
		os.Exit(1)
	}
	fmt.Println("every exercised mutant was killed")
	return nil
}

func printMutants(mutants []engine.MutantResult) {
	for _, m := range mutants {
		fmt.Printf("  %-14s %s\n  %-14s %s\n\n", m.Mutation, mutantVerdict(m), "", m.Mutation.Describe())
	}
}

func countSurvivors(mutants []engine.MutantResult) int {
	n := 0
	for _, m := range mutants {
		if m.Outcome() == engine.MutantSurvived {
			n++
		}
	}
	return n
}
