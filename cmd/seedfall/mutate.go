package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMutateSeeds = "1-100"
	defaultAppMutants  = 10
)

type mutateArgs struct {
	configArgs
	Seeds    string `json:"seeds,omitempty" jsonschema:"seed range each mutant is swept over, default 1-100"`
	Only     string `json:"only,omitempty" jsonschema:"comma-separated mutants to run, default all"`
	App      bool   `json:"app,omitempty" jsonschema:"mutate the node packages' source instead: flip comparisons, skip early returns on non-nil errors and skip Sync calls, one rebuild per mutant"`
	Mutants  int    `json:"mutants,omitempty" jsonschema:"with app, how many source mutants to pick from the seed, default 10"`
	Markdown string `json:"-"`
	jobs     int
}

type mutantView struct {
	Mutant      string  `json:"mutant"`
	Location    string  `json:"location,omitempty"`
	Description string  `json:"description"`
	Outcome     string  `json:"outcome"`
	KilledBy    *uint64 `json:"killed_by_seed,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	SeedsPassed int     `json:"seeds_passed"`
	Weakened    int64   `json:"weakened,omitempty"`
	BuildError  string  `json:"build_error,omitempty"`
}

type mutateView struct {
	Mutants  []mutantView `json:"mutants"`
	Survived int          `json:"survived"`
}

func cmdMutate(args []string) error {
	fs := newFlags("mutate")
	var a mutateArgs
	a.register(fs)
	fs.StringVar(&a.Seeds, "seeds", defaultMutateSeeds, "seed or seed range for each mutant")
	fs.IntVar(&a.jobs, "j", runtime.NumCPU(), "parallel runs")
	fs.StringVar(&a.Only, "only", "", "comma-separated mutants to run (default: all)")
	fs.BoolVar(&a.App, "app", false, "mutate the node packages' source instead of the simulated disk, network and clock")
	fs.IntVar(&a.Mutants, "mutants", defaultAppMutants, "with -app, how many source mutants to pick from the first seed")
	fs.StringVar(&a.Markdown, "md", "", "write a Markdown table of the mutants to this file, for a pull request or job summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := runMutate(a)
	if err != nil {
		return err
	}
	if a.Markdown != "" {
		if err := os.WriteFile(a.Markdown, []byte(out.markdown(a)), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("baseline: seeds %s pass\n\n", a.Seeds)
	for _, m := range out.Mutants {
		fmt.Printf("  %-21s %s\n  %-21s %s\n\n", cmp.Or(m.Location, m.Mutant), verdict(m), "", m.Description)
	}
	if out.Survived > 0 {
		fmt.Printf("%d mutants survived: your workload does not notice those guarantees disappearing\n", out.Survived)
		os.Exit(1)
	}
	fmt.Println("every exercised mutant was killed")
	return nil
}

func toolMutate(_ context.Context, _ *mcp.CallToolRequest, a mutateArgs) (*mcp.CallToolResult, mutateView, error) {
	a.Seeds, a.Mutants, a.jobs = cmp.Or(a.Seeds, defaultMutateSeeds), cmp.Or(a.Mutants, defaultAppMutants), runtime.NumCPU()
	out, err := runMutate(a)
	return nil, out, err
}

func verdict(m mutantView) string {
	switch engine.MutantOutcome(m.Outcome) {
	case engine.MutantKilled:
		return fmt.Sprintf("killed     seed %d: %s", *m.KilledBy, m.Reason)
	case engine.MutantUnexercised:
		return "not exercised (the operation never ran, or has no effect in this configuration)"
	case engine.MutantUnbuilt:
		return "did not build: " + strings.SplitN(m.BuildError, "\n", 2)[0]
	case engine.MutantSurvived:
		if m.Location != "" {
			return fmt.Sprintf("SURVIVED   %d seeds passed (or the mutated code never ran)", m.SeedsPassed)
		}
	}
	return fmt.Sprintf("SURVIVED   %d seeds passed although the operation was weakened %d times", m.SeedsPassed, m.Weakened)
}

func (v mutateView) markdown(a mutateArgs) string {
	state := "PASS"
	if v.Survived > 0 {
		state = "FAIL"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### seedfall mutate: %s\n\nConfig `%s`, seeds `%s`, %d mutants survived\n\n| Mutant | Result |\n|---|---|\n", state, a.ref(), a.Seeds, v.Survived)
	for _, m := range v.Mutants {
		fmt.Fprintf(&b, "| %s | %s |\n", cmp.Or(m.Location, m.Mutant), strings.ReplaceAll(strings.Join(strings.Fields(verdict(m)), " "), "|", `\|`))
	}
	return b.String()
}

func runMutate(a mutateArgs) (mutateView, error) {
	lo, hi, err := engine.ParseSeeds(a.Seeds)
	if err != nil {
		return mutateView{}, err
	}
	if a.App && a.Only != "" {
		return mutateView{}, errors.New("-only picks runtime mutants and cannot be combined with -app")
	}
	mutations, err := engine.ParseMutations(a.Only)
	if err != nil {
		return mutateView{}, err
	}
	r, _, err := load(a.ref())
	if err != nil {
		return mutateView{}, err
	}
	opts := engine.SweepOptions{Jobs: a.jobs}
	if base := engine.Sweep(r, lo, hi, opts); base.First != nil {
		return mutateView{}, fmt.Errorf("the unmutated system already fails on seed %d (%s); fix that first", base.First.Seed, base.First.Reason)
	}
	var out mutateView
	if a.App {
		all, err := engine.AppMutants(r)
		if err != nil {
			return mutateView{}, err
		}
		for _, m := range engine.SweepAppMutants(r, engine.SelectAppMutants(all, a.Mutants, lo), lo, hi, opts) {
			out.Mutants = append(out.Mutants, appMutantView(m))
		}
	} else {
		for _, m := range engine.SweepMutants(r, mutations, lo, hi, opts) {
			out.Mutants = append(out.Mutants, runtimeMutantView(m))
		}
	}
	for _, m := range out.Mutants {
		if engine.MutantOutcome(m.Outcome) == engine.MutantSurvived {
			out.Survived++
		}
	}
	return out, nil
}

func runtimeMutantView(m engine.MutantResult) mutantView {
	v := mutantView{Mutant: string(m.Mutation), Description: m.Mutation.Describe(), Outcome: string(m.Outcome()), SeedsPassed: m.Passed, Weakened: m.Weakened}
	if m.KilledBy != nil {
		v.KilledBy, v.Reason = &m.KilledBy.Seed, m.KilledBy.Reason
	}
	return v
}

func appMutantView(m engine.AppMutantResult) mutantView {
	v := mutantView{Mutant: m.Mutant.Op, Location: m.Mutant.Location, Description: m.Mutant.From + " -> " + m.Mutant.To, Outcome: string(m.Outcome()), SeedsPassed: m.Passed, BuildError: m.BuildError}
	if m.KilledBy != nil {
		v.KilledBy, v.Reason = &m.KilledBy.Seed, m.KilledBy.Reason
	}
	return v
}
