package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultMutateSeeds = "1-100"

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
	seeds := fs.String("seeds", defaultMutateSeeds, "seed or seed range for each mutant")
	jobs := fs.Int("j", runtime.NumCPU(), "parallel runs")
	only := fs.String("only", "", "comma-separated mutants to run (default: all)")
	fs.Parse(args)
	mutants, err := runMutants(*conf, *seeds, *only, *jobs)
	if err != nil {
		return err
	}
	fmt.Printf("baseline: seeds %s pass\n\n", *seeds)
	printMutants(mutants)
	if survived := countSurvivors(mutants); survived > 0 {
		fmt.Printf("%d mutants survived: your workload does not notice those guarantees disappearing\n", survived)
		os.Exit(1)
	}
	fmt.Println("every exercised mutant was killed")
	return nil
}

func runMutants(conf, seeds, only string, jobs int) ([]engine.MutantResult, error) {
	lo, hi, err := engine.ParseSeeds(seeds)
	if err != nil {
		return nil, err
	}
	mutations, err := engine.ParseMutations(only)
	if err != nil {
		return nil, err
	}
	r, _, err := load(conf)
	if err != nil {
		return nil, err
	}
	opts := engine.SweepOptions{Jobs: jobs}
	if base := engine.Sweep(r, lo, hi, opts); base.First != nil {
		return nil, fmt.Errorf("the unmutated system already fails on seed %d (%s); fix that first", base.First.Seed, base.First.Reason)
	}
	return engine.SweepMutants(r, mutations, lo, hi, opts), nil
}

type mutateArgs struct {
	Config string `json:"config" jsonschema:"path to seedfall.json"`
	Seeds  string `json:"seeds,omitempty" jsonschema:"seed range each mutant is swept over, default 1-100"`
	Only   string `json:"only,omitempty" jsonschema:"comma-separated mutants to run, default all"`
}

type mutantView struct {
	Mutant      string  `json:"mutant"`
	Description string  `json:"description"`
	Outcome     string  `json:"outcome"`
	KilledBy    *uint64 `json:"killed_by_seed,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	SeedsPassed int     `json:"seeds_passed"`
	Weakened    int64   `json:"weakened"`
}

type mutateView struct {
	Mutants  []mutantView `json:"mutants"`
	Survived int          `json:"survived"`
}

func toolMutate(_ context.Context, _ *mcp.CallToolRequest, a mutateArgs) (*mcp.CallToolResult, mutateView, error) {
	mutants, err := runMutants(a.Config, cmp.Or(a.Seeds, defaultMutateSeeds), a.Only, runtime.NumCPU())
	if err != nil {
		return nil, mutateView{}, err
	}
	out := mutateView{Survived: countSurvivors(mutants)}
	for _, m := range mutants {
		v := mutantView{Mutant: string(m.Mutation), Description: m.Mutation.Describe(), Outcome: string(m.Outcome()), SeedsPassed: m.Passed, Weakened: m.Weakened}
		if m.KilledBy != nil {
			v.KilledBy, v.Reason = &m.KilledBy.Seed, m.KilledBy.Reason
		}
		out.Mutants = append(out.Mutants, v)
	}
	return nil, out, nil
}

func printMutants(mutants []engine.MutantResult) {
	for _, m := range mutants {
		fmt.Printf("  %-21s %s\n  %-21s %s\n\n", m.Mutation, mutantVerdict(m), "", m.Mutation.Describe())
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
