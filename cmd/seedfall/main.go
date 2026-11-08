package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
)

const usage = `seedfall: deterministic simulation testing for Go

usage:
  seedfall init   [-dir .] [-c seedfall.json] [-node ./cmd/server]... [-workload seedfall/workload] [-n] [-json]
  seedfall run    [-c seedfall.json] [-seeds 1-1000] [-j N] [-v] [-check] [-report report.html | -json]
  seedfall replay [-c seedfall.json] -seed N [-faults file.json]
  seedfall shrink [-c seedfall.json] -seed N [-j N] [-budget 500 | -budget 10m]
  seedfall cause  [-c seedfall.json] -seed N [-points 16] [-branches 20]
  seedfall inspect [-c seedfall.json] -seed N [-faults file.json] [-at DURATION] [-node name] [-cat path | -out dir]
  seedfall mutate [-c seedfall.json] [-seeds 1-100]
  seedfall check  -history .seedfall/seed-N.history.jsonl [-model kv] [-timeline file.html]
  seedfall verify [-c seedfall.json] [-seed N [-faults file.json]] [-report report.html] [-seeds range] [-check 10] [-mutate] [-md proof.md]
  seedfall mcp    serve init, run, replay, shrink, cause, inspect, check and verify to AI coding agents over MCP (stdio)
  seedfall hook   Claude Code Stop hook: sweep $SEEDFALL_HOOK_SEEDS (unset: do nothing) and block the stop on a failure
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	commands := map[string]func([]string) error{"run": cmdRun, "replay": cmdReplay, "shrink": cmdShrink, "cause": cmdCause, "inspect": cmdInspect, "mutate": cmdMutate, "verify": cmdVerify, "check": cmdCheck, "mcp": cmdMCP, "init": cmdInit, "hook": cmdHook}
	cmd, ok := commands[os.Args[1]]
	if !ok {
		fmt.Print(usage)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "seedfall:", err)
		os.Exit(1)
	}
}

func load(path string) (*engine.Runner, *engine.Config, error) {
	cfg, err := engine.LoadConfig(path)
	if err != nil {
		return nil, nil, err
	}
	if err := engine.Build(cfg); err != nil {
		return nil, nil, err
	}
	r, err := engine.NewRunner(cfg)
	return r, cfg, err
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	conf := fs.String("c", "seedfall.json", "config file")
	seeds := fs.String("seeds", "1-200", "seed or seed range")
	jobs := fs.Int("j", runtime.NumCPU(), "parallel runs")
	verbose := fs.Bool("v", false, "print every seed")
	check := fs.Bool("check", false, "run every seed twice and fail if the traces differ")
	reportPath := fs.String("report", "", "sweep every seed and write an HTML triage report (plus .json) to this path")
	jsonOut := fs.Bool("json", false, "sweep every seed and print one JSON object per finished seed, then a summary object")
	fs.Parse(args)
	if *jsonOut && *reportPath != "" {
		return errors.New("-json and -report cannot be combined")
	}
	lo, hi, err := engine.ParseSeeds(*seeds)
	if err != nil {
		return err
	}
	r, cfg, err := load(*conf)
	if err != nil {
		return err
	}
	r.Doublecheck = *check
	if *jsonOut {
		return runJSON(r, lo, hi, *jobs)
	}
	started := time.Now()
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: *jobs, KeepGoing: *reportPath != "", OnResult: func(res engine.Result) {
		if *verbose {
			fmt.Printf("seed %d: %s\n", res.Seed, status(res))
		}
	}})
	took := time.Since(started).Round(time.Millisecond)
	if *reportPath != "" {
		return writeReport(r, cfg, *conf, *seeds, *reportPath, sum, took)
	}
	if sum.First != nil {
		printFailure(*sum.First, 25)
		fmt.Printf("\nclass: %s\n", r.Classify(*sum.First, engine.Schedule{}))
		fmt.Printf("\n%d seeds passed before the failure, %s\n", sum.Passed, took)
		fmt.Printf("replay: seedfall replay -c %s -seed %d\nshrink: seedfall shrink -c %s -seed %d\n", *conf, sum.First.Seed, *conf, sum.First.Seed)
		os.Exit(1)
	}
	printCoverage(sum.Coverage)
	printBuggify(sum.Buggify)
	if unmet := sum.Coverage.Unmet(); len(unmet) > 0 {
		fmt.Printf("FAIL %d properties were never reached or never true in %d seeds\n", len(unmet), sum.Passed)
		os.Exit(1)
	}
	fmt.Printf("ok  %d seeds passed (%d hit the time horizon) in %s\n", sum.Passed, sum.Unfinished, took)
	return nil
}

func printCoverage(c engine.Coverage) {
	msgs := c.Messages()
	if len(msgs) == 0 {
		return
	}
	fmt.Println("property       seeds reached  seeds true")
	for _, msg := range msgs {
		p := c[msg]
		mark := ""
		if problem := p.Problem(); problem != "" {
			mark = fmt.Sprintf("  <- %s (%s)", problem, p.Location)
		}
		fmt.Printf("  %-12s %12d %11d  %s%s\n", p.Kind, p.SeedsReached, p.SeedsTrue, msg, mark)
	}
	fmt.Println()
}

func printBuggify(c engine.BuggifySites) {
	names := c.Names()
	if len(names) == 0 {
		return
	}
	fmt.Println("buggify  seeds reached  seeds enabled  seeds fired  fired")
	for _, name := range names {
		s := c[name]
		mark := ""
		if problem := s.Problem(); problem != "" {
			mark = fmt.Sprintf("  <- %s", problem)
			if s.Location != "" {
				mark += fmt.Sprintf(" (%s)", s.Location)
			}
		}
		fmt.Printf("  %20d %14d %12d %6d  %s%s\n", s.SeedsReached, s.SeedsEnabled, s.SeedsFired, s.Fired, name, mark)
	}
	fmt.Println()
}

func writeReport(r *engine.Runner, cfg *engine.Config, conf, seeds, path string, sum engine.Summary, took time.Duration) error {
	in := report.Input{Config: conf, Seeds: seeds, Faults: cfg.Faults, Summary: sum, Elapsed: took, Replay: func(seed uint64) engine.Result { return r.Run(seed, nil) }}
	in.Classify = func(res engine.Result) engine.Classification { return r.Classify(res, engine.Schedule{}) }
	rep, err := report.Generate(in, path, filepath.Join(cfg.Dir, ".seedfall", "history.jsonl"))
	if err != nil {
		return err
	}
	printCoverage(sum.Coverage)
	printBuggify(sum.Buggify)
	fmt.Printf("%d of %d seeds failed, %d findings, in %s\n", rep.Failed, rep.Total, len(rep.Findings), took)
	for _, f := range rep.Findings {
		fmt.Printf("  %-9s %-40s %5d seeds  e.g. seed %d  %s\n", f.Status, f.Key, f.Count, f.Seeds[0], f.Classification)
	}
	for _, f := range rep.Cleared {
		fmt.Printf("  %-9s %-40s %s\n", f.Status, f.Key, f.Note)
	}
	fmt.Printf("report: %s\n", path)
	if rep.Failed > 0 || len(sum.Coverage.Unmet()) > 0 {
		os.Exit(1)
	}
	return nil
}

func status(res engine.Result) string {
	switch {
	case res.Failed:
		return "FAIL " + res.Reason
	case !res.Finished:
		return "ok (horizon)"
	}
	return "ok"
}

func printFailure(res engine.Result, traceLines int) {
	defer saveHistory(res).print()
	fmt.Printf("FAIL seed %d at t=%s (%d scheduler steps, %s real)\n  %s\n\n", res.Seed, res.At, res.Steps, res.Elapsed.Round(time.Millisecond), res.Reason)
	happened := res.FaultsBefore()
	fmt.Printf("faults before the failure (%d):\n", len(happened))
	for _, f := range happened {
		fmt.Printf("  %s\n", f.Describe())
	}
	lines := res.Trace
	if traceLines > 0 && len(lines) > traceLines {
		fmt.Printf("\nlast %d trace lines:\n", traceLines)
		lines = tail(lines, traceLines)
	} else {
		fmt.Println("\ntrace:")
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}

func seedFlag(fs *flag.FlagSet) *uint64 { return fs.Uint64("seed", 0, "seed to run") }

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	conf := fs.String("c", "seedfall.json", "config file")
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "fault schedule from seedfall shrink")
	fs.Parse(args)
	r, cfg, err := load(*conf)
	if err != nil {
		return err
	}
	sched, err := readSchedule(*faultsFile)
	if err != nil {
		return err
	}
	res := r.Replay(*seed, sched)
	if !res.Failed {
		for _, l := range res.Trace {
			fmt.Println(l)
		}
		fmt.Printf("\nseed %d: %s, %d steps, %s real\n", *seed, status(res), res.Steps, res.Elapsed.Round(time.Millisecond))
		if note := cutShort(res, cfg); note != "" {
			fmt.Println("note:", note)
		}
		saveHistory(res).print()
		return nil
	}
	printFailure(res, 0)
	os.Exit(1)
	return nil
}
