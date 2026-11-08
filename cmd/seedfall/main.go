package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
)

type command struct {
	name     string
	synopsis string
	run      func([]string) error
}

var commands []command

func init() {
	commands = []command{
		{"init", "[-dir .] [-c seedfall.json] [-node ./cmd/server]... [-workload seedfall/workload] [-n] [-json]", cmdInit},
		{"run", "[-c seedfall.json] [-seeds 1-1000] [-j N] [-v] [-check] [-net-trace] [-report report.html | -json] [-pinned=false] [-seeds-file file] [-rotate N] [-until-novelty N [-batch 20]] [-timeout 10m] [-cpuprofile file]", cmdRun},
		{"explore", "[-c seedfall.json] [-seeds 1-32] [-rounds N] [-batch 16] [-until-novelty 8] [-budget 10m] [-explore-seed 1] [-keep-going] [-corpus dir] [-report report.html | -json] [-cpuprofile file]", cmdExplore},
		{"replay", "[-c seedfall.json] -seed N [-faults file.json] [-net-trace] [-html timeline.html | -html timeline.mmd]", cmdReplay},
		{"logs", "[-c seedfall.json] -seed N [-faults file.json] [-node a,b] [-grep re] [-from 1s] [-to 2s] [-json] [-net-trace]", cmdLogs},
		{"debug", "[-c seedfall.json] -seed N [-faults file.json] [-at DURATION] [-net-trace]", cmdDebug},
		{"trace grep", "[-c seedfall.json] -seed N [-faults file.json] -e re [-context 3] [-node a,b] [-from 1s] [-to 2s] [-limit 200] [-json] [-net-trace]", cmdTraceGrep},
		{"trace diff", "[-context 3] <trace.txt or artifacts dir> <trace.txt or artifacts dir>", cmdTraceDiff},
		{"shrink", "[-c seedfall.json] -seed N [-faults file.json] [-j N] [-budget 500 | -budget 10m]", cmdShrink},
		{"faults", "[-c seedfall.json] -seed N [-faults file.json] [-remove 2]... [-shift 1=-50ms]... [-add '{\"at\":\"1s\",...}']... [-out file.json]", cmdFaults},
		{"cause", "[-c seedfall.json] -seed N [-points 16] [-branches 20]", cmdCause},
		{"inspect", "[-c seedfall.json] -seed N [-faults file.json] [-at DURATION] [-node name] [-cat path | -out dir]", cmdInspect},
		{"mutate", "[-c seedfall.json] [-seeds 1-100] [-only fsync_noop,net_dup_chunk,...] [-app [-mutants 10]] [-md mutants.md]", cmdMutate},
		{"check", "-history .seedfall/seed-N.history.jsonl [-model kv] [-timeline file.html]", cmdCheck},
		{"pin", "[-c seedfall.json] -seed N [-faults file.json]", cmdPin},
		{"findings", "[-report .seedfall/report.html] [-history file] [-status new,regressed] [-json]", cmdFindings},
		{"report notify", "[-report .seedfall/report.html] [-webhook URL] [-link URL] [-dry-run]", cmdNotify},
		{"report issue", "-repo owner/name [-report .seedfall/report.html] [-dry-run]", cmdIssue},
		{"report github", "[-report .seedfall/report.html] [-output file]", cmdGitHub},
		{"report comment", "-repo owner/name -pr N [-report .seedfall/report.html | -body file] [-marker name] [-link URL] [-dry-run]", cmdComment},
		{"verify", "[-c seedfall.json] [-seed N [-faults file.json]] [-report report.html] [-seeds range] [-check 10] [-mutate] [-app-mutants N] [-md proof.md]", cmdVerify},
		{"doctor", "[-json]", cmdDoctor},
		{"version", "[-json]", cmdVersion},
		{"help", "[command]", cmdHelp},
		{"schema", "print every command with its flags and every MCP tool, prompt and resource as JSON", cmdSchema},
		{"mcp", "serve init, run, explore, replay, logs, trace search, debug, shrink, faults edit, cause, inspect, check, mutate, pin, findings, verify and doctor to AI coding agents over MCP (stdio)", cmdMCP},
		{"hook", "Claude Code Stop hook: sweep $SEEDFALL_HOOK_SEEDS (unset: do nothing) and block the stop on a failure", cmdHook},
	}
}

func usage() string {
	var b strings.Builder
	b.WriteString("seedfall: deterministic simulation testing for Go\n\nusage:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  seedfall %-6s %s\n", c.name, c.synopsis)
	}
	b.WriteString(`
every command that takes -c also takes -profile name, to merge a profile from the config's profiles over it,
and -set path=value (repeatable), to override one value last, for example -set faults=resilient -set duration=10s
`)
	return b.String()
}

func findCommand(args []string) (command, []string, bool) {
	for n := min(len(args), 2); n > 0; n-- {
		name := strings.Join(args[:n], " ")
		if i := slices.IndexFunc(commands, func(c command) bool { return c.name == name }); i >= 0 {
			return commands[i], args[n:], true
		}
	}
	return command{}, nil, false
}

func main() {
	cmd, args, ok := findCommand(normalizeArgs(os.Args[1:]))
	if !ok {
		fmt.Print(usage())
		os.Exit(2)
	}
	if err := cmd.run(args); err != nil {
		fmt.Fprintln(os.Stderr, "seedfall:", err)
		os.Exit(1)
	}
}

func newFlags(name string) *flag.FlagSet {
	if collectFlags == nil {
		return flag.NewFlagSet(name, flag.ExitOnError)
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	collectFlags(fs)
	return fs
}

func load(ref engine.ConfigRef) (*engine.Runner, *engine.Config, error) {
	return loadWith(ref, false)
}

func loadWith(ref engine.ConfigRef, feedback bool) (*engine.Runner, *engine.Config, error) {
	cfg, err := engine.Load(ref)
	if err != nil {
		return nil, nil, err
	}
	cfg.Feedback = feedback
	if err := engine.Build(cfg); err != nil {
		return nil, nil, err
	}
	r, err := engine.NewRunner(cfg)
	return r, cfg, err
}

func cmdRun(args []string) error {
	fs := newFlags("run")
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
	rotate := fs.Uint64("rotate", 0, "sweep the Nth range of the same width after -seeds, so CI can pass its run number and keep exploring new seeds")
	untilNovelty := fs.Int("until-novelty", 0, "stop once this many batches of seeds in a row add no new property outcome, fault shape, buggify firing or function; 0 sweeps the whole range")
	batch := fs.Int("batch", defaultSweepBatch, "seeds per batch for -until-novelty")
	timeout := fs.Duration("timeout", 0, "stop starting seeds after this wall-clock time, for example 10m; seeds in flight finish, and pinned regressions count toward it; 0 sweeps the whole range")
	cpuProfile := fs.String("cpuprofile", "", "write a pprof CPU profile of the sweep to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conf := ca.ref().String()
	if *jsonOut && *reportPath != "" {
		return errors.New("-json and -report cannot be combined")
	}
	if *timeout < 0 {
		return errNegativeTimeout
	}
	lo, hi, err := engine.ParseSeeds(*seeds)
	if err != nil {
		return err
	}
	if *rotate > 0 {
		if lo, hi, err = engine.RotateSeeds(lo, hi, *rotate); err != nil {
			return err
		}
		*seeds = fmt.Sprintf("%d-%d", lo, hi)
		if !*jsonOut {
			fmt.Printf("rotation %d: seeds %s\n", *rotate, *seeds)
		}
	}
	r, cfg, err := loadWith(ca.ref(), *untilNovelty > 0)
	if err != nil {
		return err
	}
	stop, err := startProfile(*cpuProfile)
	if err != nil {
		return err
	}
	defer stop()
	sweep := engine.SweepOptions{Jobs: *jobs, Batch: *batch, UntilNovelty: *untilNovelty}
	r.Doublecheck, r.NetTrace = *check, *netTrace
	started := time.Now()
	sweep.Deadline = sweepDeadline(started, *timeout)
	regs, err := replayRegressions(r, cfg, *pinned, *seedsFile, *jobs)
	if err != nil {
		return err
	}
	if *jsonOut {
		return runJSON(r, conf, regs, lo, hi, sweep)
	}
	printRegressions(regs)
	if bad := engine.FirstFailed(regs); bad != nil && *reportPath == "" {
		fmt.Printf("FAIL regression %s\n", bad.String())
		printFailure(bad.Result, 25)
		a, err := regressionArtifacts(r, conf, *bad)
		printArtifacts(a, err)
		fmt.Printf("replay: %s\n", a.Replay)
		exit(1)
	}
	sweep.KeepGoing = *reportPath != ""
	sweep.OnResult = func(res engine.Result) {
		if *verbose {
			fmt.Printf("seed %d: %s\n", res.Seed, status(res))
		}
	}
	clearProgress := withProgress(&sweep, !*verbose && stderrIsTerminal())
	sum := engine.Sweep(r, lo, hi, sweep)
	clearProgress()
	took := time.Since(started).Round(time.Millisecond)
	if *reportPath != "" {
		return writeReport(r, cfg, conf, *seeds, *reportPath, sum, viewRegressions(r, conf, regs), took, true)
	}
	if sum.First != nil {
		printFailure(*sum.First, 25)
		a, _, err := r.SaveArtifacts(sum.First.Seed, engine.Schedule{}, engine.ArtifactOptions{Config: conf, Original: sum.First})
		printArtifacts(a, err)
		fmt.Printf("\n%d seeds passed before the failure, %s\n", sum.Passed, took)
		fmt.Printf("replay: seedfall replay -c %s -seed %d\nshrink: seedfall shrink -c %s -seed %d\n", conf, sum.First.Seed, conf, sum.First.Seed)
		exit(1)
	}
	printCoverage(sum.Coverage)
	printBuggify(sum.Buggify)
	printGrowth(sum)
	if unmet := sum.Coverage.Unmet(); len(unmet) > 0 {
		fmt.Printf("FAIL %d properties were never reached or never true in %d seeds\n", len(unmet), sum.Passed)
		exit(1)
	}
	fmt.Printf("ok  %d seeds passed (%d hit the time horizon) in %s\n", sum.Passed, sum.Unfinished, took)
	printEfficiency(sum)
	return nil
}

var errNegativeTimeout = errors.New("timeout cannot be negative")

func sweepDeadline(started time.Time, timeout time.Duration) time.Time {
	if timeout <= 0 {
		return time.Time{}
	}
	return started.Add(timeout)
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

func writeReport(r *engine.Runner, cfg *engine.Config, conf, seeds, path string, sum engine.Summary, regs []report.Regression, took time.Duration, failOnUnmet bool) error {
	in := report.Input{Config: conf, Seeds: seeds, Version: engine.Version(), Commit: engine.Commit(cfg.Dir), Faults: cfg.Faults, Summary: sum, Elapsed: took}
	in.Replay = func(failed engine.Result) report.Replayed {
		s, opts := engine.Schedule{}, engine.ArtifactOptions{Config: conf}
		if failed.Scheduled {
			s, opts = failed.Schedule(engine.Schedule{}), exploreArtifact(conf, &failed)
			opts.Original = nil
		}
		a, res, err := r.SaveArtifacts(failed.Seed, s, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seedfall: artifacts of seed %d: %v\n", failed.Seed, err)
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
	printGrowth(sum)
	printEfficiency(sum)
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
	if rep.Failed > 0 || rep.RegressionsFailed() > 0 || (failOnUnmet && len(sum.Coverage.Unmet()) > 0) {
		exit(1)
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
	fs := newFlags("replay")
	ca := configFlags(fs)
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "fault schedule from seedfall shrink")
	htmlOut := fs.String("html", "", "write the run's timeline to this file: an HTML space-time diagram with a log explorer, or a Mermaid sequence diagram for a .mmd path")
	netTrace := fs.Bool("net-trace", false, "also trace every connection, message sent and message read")
	if err := fs.Parse(args); err != nil {
		return err
	}
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
