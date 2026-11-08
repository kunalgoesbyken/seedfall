package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
	"github.com/kunalgoesbyken/seedfall/verify"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultCheckSeeds = 10

type verifyArgs struct {
	Config     string  `json:"config" jsonschema:"path to seedfall.json"`
	Seed       *uint64 `json:"seed,omitempty" jsonschema:"failing seed that the change should fix"`
	Faults     string  `json:"faults,omitempty" jsonschema:"fault schedule file for that seed, written by seedfall_shrink"`
	Report     string  `json:"report,omitempty" jsonschema:"triage report from seedfall run -report; every finding in it is replayed"`
	Seeds      string  `json:"seeds,omitempty" jsonschema:"fresh seed range to sweep; default is enough seeds for 95% confidence at the historical failure rate"`
	CheckSeeds int     `json:"check_seeds,omitempty" jsonschema:"fresh seeds to run twice for the determinism check, default 10"`
	Mutate     bool    `json:"mutate,omitempty" jsonschema:"also require every exercised mutant to be killed"`
	Markdown   string  `json:"-"`
	jobs       int
}

type verifyView struct {
	verify.Proof
	Markdown string `json:"markdown"`
}

func cmdVerify(args []string) error {
	a, err := parseVerify(args)
	if err != nil {
		return err
	}
	proof, err := runVerify(a)
	if err != nil {
		return err
	}
	fmt.Print(proof.Text())
	if !proof.Passed {
		os.Exit(1)
	}
	return nil
}

func toolVerify(_ context.Context, _ *mcp.CallToolRequest, a verifyArgs) (*mcp.CallToolResult, verifyView, error) {
	a.CheckSeeds = cmp.Or(a.CheckSeeds, defaultCheckSeeds)
	a.jobs = runtime.NumCPU()
	if err := a.validate(); err != nil {
		return nil, verifyView{}, err
	}
	proof, err := runVerify(a)
	if err != nil {
		return nil, verifyView{}, err
	}
	return nil, verifyView{Proof: proof, Markdown: proof.Markdown()}, nil
}

func parseVerify(args []string) (verifyArgs, error) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	var a verifyArgs
	fs.StringVar(&a.Config, "c", "seedfall.json", "config file")
	seed := seedFlag(fs)
	fs.StringVar(&a.Faults, "faults", "", "fault schedule for -seed, from seedfall shrink")
	fs.StringVar(&a.Report, "report", "", "triage report from run -report; replay every finding in it")
	fs.StringVar(&a.Seeds, "seeds", "", "fresh seed range to sweep (default: enough for 95% confidence at the historical failure rate)")
	fs.IntVar(&a.CheckSeeds, "check", defaultCheckSeeds, "fresh seeds to run twice for the determinism check")
	fs.BoolVar(&a.Mutate, "mutate", false, "also require every exercised mutant to be killed")
	fs.StringVar(&a.Markdown, "md", "", "write a Markdown proof block for the pull request to this file")
	fs.IntVar(&a.jobs, "j", runtime.NumCPU(), "parallel runs")
	fs.Parse(args)
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "seed" {
			a.Seed = seed
		}
	})
	return a, a.validate()
}

func (a verifyArgs) validate() error {
	switch {
	case a.Config == "":
		return errors.New("config is required")
	case a.Seed == nil && a.Report == "":
		return errors.New("nothing to verify: pass -seed N (with optional -faults) and/or -report")
	case a.Faults != "" && a.Seed == nil:
		return errors.New("-faults needs -seed")
	case a.CheckSeeds < 1:
		return errors.New("-check must be at least 1")
	}
	return nil
}

func (a verifyArgs) command() string {
	parts := []string{"seedfall verify -c", a.Config}
	if a.Seed != nil {
		parts = append(parts, "-seed", strconv.FormatUint(*a.Seed, 10))
	}
	for _, kv := range [][2]string{{"-faults", a.Faults}, {"-report", a.Report}, {"-seeds", a.Seeds}} {
		if kv[1] != "" {
			parts = append(parts, kv[0], kv[1])
		}
	}
	if a.CheckSeeds != defaultCheckSeeds {
		parts = append(parts, "-check", strconv.Itoa(a.CheckSeeds))
	}
	if a.Mutate {
		parts = append(parts, "-mutate")
	}
	return strings.Join(parts, " ")
}

func runVerify(a verifyArgs) (verify.Proof, error) {
	targets, last, err := verifyTargets(a)
	if err != nil {
		return verify.Proof{}, err
	}
	plan, err := verifyPlan(a, targets, last)
	if err != nil {
		return verify.Proof{}, err
	}
	r, _, err := load(a.Config)
	if err != nil {
		return verify.Proof{}, err
	}
	proof := verify.Run(r, plan)
	proof.Config, proof.Command = a.Config, a.command()
	if a.Markdown == "" {
		return proof, nil
	}
	return proof, os.WriteFile(a.Markdown, []byte(proof.Markdown()), 0o644)
}

func verifyPlan(a verifyArgs, targets []verify.Target, last uint64) (verify.Plan, error) {
	plan := verify.Plan{Targets: targets, CheckSeeds: a.CheckSeeds, Mutate: a.Mutate, Jobs: a.jobs}
	if a.Seeds != "" {
		lo, hi, err := engine.ParseSeeds(a.Seeds)
		plan.Lo, plan.Hi = lo, hi
		return plan, err
	}
	n, rate := verify.SeedsNeeded(targets)
	plan.Lo, plan.Hi, plan.Rate = last+1, last+uint64(n), rate
	return plan, nil
}

func verifyTargets(a verifyArgs) ([]verify.Target, uint64, error) {
	var targets []verify.Target
	var last uint64
	if a.Seed != nil {
		sched, err := readSchedule(a.Faults)
		if err != nil {
			return nil, 0, err
		}
		targets = append(targets, verify.Target{Label: scheduleLabel(*a.Seed, a.Faults), Seed: *a.Seed, Schedule: sched})
		last = *a.Seed
	}
	if a.Report == "" {
		return targets, last, nil
	}
	found, swept, err := reportTargets(a.Config, a.Report)
	return append(targets, found...), max(last, swept), err
}

func reportTargets(config, path string) ([]verify.Target, uint64, error) {
	rep, err := report.Load(path)
	if err != nil {
		return nil, 0, err
	}
	_, swept, err := engine.ParseSeeds(rep.Seeds)
	if err != nil {
		return nil, 0, err
	}
	var targets []verify.Target
	for _, f := range rep.Findings {
		for _, seed := range f.Seeds {
			targets = append(targets, verify.Target{Label: fmt.Sprintf("%s %s", f.Key, scheduleLabel(seed, "")), Seed: seed, Rate: f.Rate})
			saved, err := savedSchedule(config, seed, f)
			if err != nil {
				return nil, 0, err
			}
			targets = append(targets, saved...)
		}
	}
	return targets, swept, nil
}

func savedSchedule(config string, seed uint64, f report.Finding) ([]verify.Target, error) {
	path := schedulePath(config, seed)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	sched, err := readSchedule(path)
	if err != nil {
		return nil, err
	}
	return []verify.Target{{Label: fmt.Sprintf("%s %s", f.Key, scheduleLabel(seed, path)), Seed: seed, Schedule: sched, Rate: f.Rate}}, nil
}

func scheduleLabel(seed uint64, faultsFile string) string {
	if faultsFile == "" {
		return fmt.Sprintf("seed %d", seed)
	}
	return fmt.Sprintf("seed %d with %s", seed, faultsFile)
}
