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
  seedfall run    [-c seedfall.json] [-seeds 1-1000] [-j N] [-v] [-check] [-net-trace] [-report report.html | -json] [-pinned=false] [-seeds-file file]
  seedfall replay [-c seedfall.json] -seed N [-faults file.json] [-net-trace] [-html timeline.html | -html timeline.mmd]
  seedfall logs   [-c seedfall.json] -seed N [-faults file.json] [-node a,b] [-grep re] [-from 1s] [-to 2s] [-json] [-net-trace]
  seedfall debug  [-c seedfall.json] -seed N [-faults file.json] [-at DURATION] [-net-trace]
  seedfall trace diff <trace.txt or artifacts dir> <trace.txt or artifacts dir> [-context 3]
  seedfall shrink [-c seedfall.json] -seed N [-j N] [-budget 500 | -budget 10m]
  seedfall cause  [-c seedfall.json] -seed N [-points 16] [-branches 20]
  seedfall inspect [-c seedfall.json] -seed N [-faults file.json] [-at DURATION] [-node name] [-cat path | -out dir]
  seedfall mutate [-c seedfall.json] [-seeds 1-100] [-only fsync_noop,net_dup_chunk,...] [-app [-mutants 10]]
  seedfall check  -history .seedfall/seed-N.history.jsonl [-model kv] [-timeline file.html]
  seedfall pin    [-c seedfall.json] -seed N [-faults file.json]
  seedfall report notify [-report .seedfall/report.html] [-webhook URL] [-link URL] [-dry-run]
  seedfall report issue  -repo owner/name [-report .seedfall/report.html] [-dry-run]
  seedfall verify [-c seedfall.json] [-seed N [-faults file.json]] [-report report.html] [-seeds range] [-check 10] [-mutate] [-app-mutants N] [-md proof.md]
  seedfall mcp    serve init, run, replay, logs, debug, shrink, cause, inspect, check, mutate, pin, findings and verify to AI coding agents over MCP (stdio)
  seedfall hook   Claude Code Stop hook: sweep $SEEDFALL_HOOK_SEEDS (unset: do nothing) and block the stop on a failure

every command that takes -c also takes -profile name, to merge a profile from the config's profiles over it,
and -set path=value (repeatable), to override one value last, for example -set faults=resilient -set duration=10s
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	commands := map[string]func([]string) error{"run": cmdRun, "replay": cmdReplay, "shrink": cmdShrink, "cause": cmdCause, "inspect": cmdInspect, "mutate": cmdMutate, "verify": cmdVerify, "check": cmdCheck, "mcp": cmdMCP, "init": cmdInit, "hook": cmdHook, "pin": cmdPin, "report": cmdReportFindings, "logs": cmdLogs, "trace": cmdTrace, "debug": cmdDebug}
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

func load(ref engine.ConfigRef) (*engine.Runner, *engine.Config, error) {
	cfg, err := engine.Load(ref)
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
	ca := configFlags(fs)
	seeds := fs.String("seeds", "1-200", "seed or seed range")
	jobs := fs.Int("j", runtime.NumCPU(), "parallel runs")
	verbose := fs.Bool("v", false, "print every seed")
	check := fs.Bool("check", false, "run every seed twice and fail at the first host call or trace line that differs")
	reportPath := fs.String("report", "", "sweep every seed and write an HTML triage report (plus .json) to this path")
	jsonOut := fs.Bool("json", false, "sweep every seed and print one JSON object per finished seed, then a summary object")
	netTrace := fs.Bool("net-trace", false, "also trace every connection, message sent and message read")
	pinned := fs.Bool("pinned", true, "replay the schedules pinned in "+engine.RegressionsDir+"/ before the sweep")
	seedsFile := fs.String("seeds-file", "", "file of known-bad seeds, one or more per line with # comments, replayed before the sweep")
	fs.Parse(args)
	conf := ca.ref().String()
	if *jsonOut && *reportPath != "" {
		return errors.New("-json and -report cannot be combined")
	}
	lo, hi, err := engine.ParseSeeds(*seeds)
	if err != nil {
		return err
	}
	r, cfg, err := load(ca.ref())
	if err != nil {
		return err
	}
	r.Doublecheck, r.NetTrace = *check, *netTrace
	started := time.Now()
	regs, err := replayRegressions(r, cfg, *pinned, *seedsFile, *jobs)
	if err != nil {
		return err
	}
	if *jsonOut {
		return runJSON(r, conf, regs, lo, hi, *jobs)
	}
	printRegressions(regs)
	if bad := engine.FirstFailed(regs); bad != nil && *reportPath == "" {
		fmt.Printf("FAIL regression %s\n", bad.String())
		printFailure(bad.Result, 25)
		a, err := regressionArtifacts(r, conf, *bad)
		printArtifacts(a, err)
		fmt.Printf("replay: %s\n", a.Replay)
		os.Exit(1)
	}
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: *jobs, KeepGoing: *reportPath != "", OnResult: func(res engine.Result) {
		if *verbose {
			fmt.Printf("seed %d: %s\n", res.Seed, status(res))
		}
	}})
	took := time.Since(started).Round(time.Millisecond)
	if *reportPath != "" {
		return writeReport(r, cfg, conf, *seeds, *reportPath, sum, viewRegressions(r, conf, regs), took)
	}
	if sum.First != nil {
		printFailure(*sum.First, 25)
		a, _, err := r.SaveArtifacts(sum.First.Seed, engine.Schedule{}, engine.ArtifactOptions{Config: conf, Original: sum.First})
		printArtifacts(a, err)
		fmt.Printf("\n%d seeds passed before the failure, %s\n", sum.Passed, took)
		fmt.Printf("replay: seedfall replay -c %s -seed %d\nshrink: seedfall shrink -c %s -seed %d\n", conf, sum.First.Seed, conf, sum.First.Seed)
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
		if note := p.Kind.MarginNote(p.Margin); note != "" {
			mark = fmt.Sprintf("  (%s)", note)
		}
		if problem := p.Problem(); problem != "" {
			mark += fmt.Sprintf("  <- %s (%s)", problem, p.Location)
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

func writeReport(r *engine.Runner, cfg *engine.Config, conf, seeds, path string, sum engine.Summary, regs []report.Regression, took time.Duration) error {
	in := report.Input{Config: conf, Seeds: seeds, Version: engine.Version(), Commit: engine.Commit(cfg.Dir), Faults: cfg.Faults, Summary: sum, Elapsed: took}
	in.Replay = func(seed uint64) report.Replayed {
		a, res, err := r.SaveArtifacts(seed, engine.Schedule{}, engine.ArtifactOptions{Config: conf})
		if err != nil {
			fmt.Fprintf(os.Stderr, "seedfall: artifacts of seed %d: %v\n", seed, err)
			a.Dir = ""
		}
		return report.Replayed{Result: res, Class: a.Classification, Artifacts: filepath.ToSlash(a.Dir)}
	}
	in.Regressions = regs
	rep, err := report.Generate(in, path, filepath.Join(cfg.Dir, ".seedfall", "history.jsonl"))
	if err != nil {
		return err
	}
	printCoverage(sum.Coverage)
	printBuggify(sum.Buggify)
	for _, reg := range rep.Regressions {
		if !reg.Passed {
			fmt.Printf("FAIL regression seed %d from %s: %s\n  artifacts: %s\n", reg.Seed, reg.Source, reg.Reason, reg.Artifacts)
		}
	}
	fmt.Printf("%d of %d seeds failed, %d findings, in %s\n", rep.Failed, rep.Total, len(rep.Findings), took)
	for _, f := range rep.Findings {
		fmt.Printf("  %-9s %-40s %5d seeds  e.g. seed %d  %s\n", f.Status, f.Key, f.Count, f.Seeds[0], f.Classification)
	}
	for _, f := range rep.Cleared {
		fmt.Printf("  %-9s %-40s %s\n", f.Status, f.Key, f.Note)
	}
	fmt.Printf("report: %s\n", path)
	if rep.Failed > 0 || rep.RegressionsFailed() > 0 || len(sum.Coverage.Unmet()) > 0 {
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
	ca := configFlags(fs)
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "fault schedule from seedfall shrink")
	htmlOut := fs.String("html", "", "write the run's timeline to this file: an HTML space-time diagram with a log explorer, or a Mermaid sequence diagram for a .mmd path")
	netTrace := fs.Bool("net-trace", false, "also trace every connection, message sent and message read")
	fs.Parse(args)
	conf := ca.ref().String()
	r, cfg, err := load(ca.ref())
	if err != nil {
		return err
	}
	sched, err := readSchedule(*faultsFile)
	if err != nil {
		return err
	}
	r.NetTrace = *netTrace
	res := replayOrRecord(r, *seed, sched, *htmlOut != "")
	if *htmlOut != "" {
		if err := writeTimeline(*htmlOut, res, timelineTitle(cfg, *seed)); err != nil {
			return err
		}
		fmt.Printf("timeline: %s\n", *htmlOut)
	}
	if !res.Failed {
		for _, l := range res.Trace {
			fmt.Println(l)
		}
		fmt.Printf("\nseed %d: %s, %d steps, %s real\n", *seed, status(res), res.Steps, res.Elapsed.Round(time.Millisecond))
		fmt.Printf("determinism hash %s over %d host calls\n", res.Hash(), res.HostCalls)
		if note := cutShort(res, cfg); note != "" {
			fmt.Println("note:", note)
		}
		saveHistory(res).print()
		return nil
	}
	printFailure(res, 0)
	fmt.Printf("\ndeterminism hash %s over %d host calls\n", res.Hash(), res.HostCalls)
	opts := engine.ArtifactOptions{Config: conf, ScheduleFile: *faultsFile, Original: &res}
	if *faultsFile != "" {
		opts.Label = "schedule"
	}
	a, _, err := r.SaveArtifacts(*seed, sched, opts)
	printArtifacts(a, err)
	os.Exit(1)
	return nil
}

func replayOrRecord(r *engine.Runner, seed uint64, s engine.Schedule, record bool) engine.Result {
	if record {
		return r.Record(seed, s)
	}
	return r.Replay(seed, s)
}

func printArtifacts(a engine.Artifact, err error) {
	if a.Classification.Verdict != "" {
		fmt.Printf("\nclass: %s\n", a.Classification)
	}
	if err != nil {
		fmt.Printf("artifacts: not written: %v\n", err)
		return
	}
	fmt.Printf("artifacts: %s\n", a.Dir)
}
