package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const usage = `seedfall: deterministic simulation testing for Go

usage:
  seedfall run    [-c seedfall.json] [-seeds 1-1000] [-j N] [-v] [-check]
  seedfall replay [-c seedfall.json] -seed N [-faults file.json]
  seedfall shrink [-c seedfall.json] -seed N
  seedfall mcp    serve run, replay and shrink to AI coding agents over MCP (stdio)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	commands := map[string]func([]string) error{"run": cmdRun, "replay": cmdReplay, "shrink": cmdShrink, "mcp": cmdMCP}
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
	fs.Parse(args)
	lo, hi, err := engine.ParseSeeds(*seeds)
	if err != nil {
		return err
	}
	r, _, err := load(*conf)
	if err != nil {
		return err
	}
	r.Doublecheck = *check
	started := time.Now()
	sum := engine.Sweep(r, lo, hi, *jobs, func(res engine.Result) {
		if *verbose {
			fmt.Printf("seed %d: %s\n", res.Seed, status(res))
		}
	})
	took := time.Since(started).Round(time.Millisecond)
	if sum.First != nil {
		report(*sum.First, 25)
		fmt.Printf("\n%d seeds passed before the failure, %s\n", sum.Passed, took)
		fmt.Printf("replay: seedfall replay -c %s -seed %d\nshrink: seedfall shrink -c %s -seed %d\n", *conf, sum.First.Seed, *conf, sum.First.Seed)
		os.Exit(1)
	}
	printCoverage(sum.Coverage)
	if unmet := sum.Coverage.Unmet(); len(unmet) > 0 {
		fmt.Printf("FAIL %d properties were never satisfied in %d seeds\n", len(unmet), sum.Passed)
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
		if p.Unmet() {
			mark = "  <- never satisfied"
		}
		fmt.Printf("  %-12s %12d %11d  %s%s\n", p.Kind, p.SeedsReached, p.SeedsTrue, msg, mark)
	}
	fmt.Println()
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

func saveHistory(res engine.Result) {
	if !res.History.Checked || res.History.Ok {
		return
	}
	path := filepath.Join(".seedfall", fmt.Sprintf("seed-%d.history.html", res.Seed))
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	if res.History.Visualize(f) == nil {
		fmt.Printf("\nlinearizability timeline: %s\n", path)
	}
}

func report(res engine.Result, traceLines int) {
	defer saveHistory(res)
	fmt.Printf("FAIL seed %d at t=%s (%d scheduler steps, %s real)\n  %s\n\n", res.Seed, res.At, res.Steps, res.Elapsed.Round(time.Millisecond), res.Reason)
	happened := faultsBefore(res)
	fmt.Printf("faults before the failure (%d):\n", len(happened))
	for _, f := range happened {
		fmt.Printf("  %s\n", describe(f))
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

func faultsBefore(res engine.Result) []engine.Fault {
	return slices.DeleteFunc(slices.Clone(res.Faults), func(f engine.Fault) bool { return time.Duration(f.At) > res.At })
}

func describe(f engine.Fault) string {
	at := func(d engine.Duration) time.Duration { return time.Duration(d).Round(time.Microsecond) }
	switch f.Kind {
	case engine.FaultFsyncEIO:
		return fmt.Sprintf("%8s  fsync on %s fails with EIO at disk op #%d, unsynced writes dropped", at(f.At), f.Node, f.Op)
	case engine.FaultCrashIO:
		return fmt.Sprintf("%8s  crash %s during disk op #%d, restart at %s", at(f.At), f.Node, f.Op, at(f.Until))
	case engine.FaultCrash:
		return fmt.Sprintf("%8s  crash %s, restart at %s", at(f.At), f.Node, at(f.Until))
	}
	return fmt.Sprintf("%8s  partition %v from the rest until %s", at(f.At), f.Side, at(f.Until))
}

func seedFlag(fs *flag.FlagSet) *uint64 { return fs.Uint64("seed", 0, "seed to run") }

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	conf := fs.String("c", "seedfall.json", "config file")
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "fault schedule from seedfall shrink")
	fs.Parse(args)
	r, _, err := load(*conf)
	if err != nil {
		return err
	}
	faults, err := readFaults(*faultsFile)
	if err != nil {
		return err
	}
	res := r.Run(*seed, faults)
	if !res.Failed {
		for _, l := range res.Trace {
			fmt.Println(l)
		}
		fmt.Printf("\nseed %d: %s, %d steps, %s real\n", *seed, status(res), res.Steps, res.Elapsed.Round(time.Millisecond))
		return nil
	}
	report(res, 0)
	os.Exit(1)
	return nil
}

func cmdShrink(args []string) error {
	fs := flag.NewFlagSet("shrink", flag.ExitOnError)
	conf := fs.String("c", "seedfall.json", "config file")
	seed := seedFlag(fs)
	fs.Parse(args)
	r, _, err := load(*conf)
	if err != nil {
		return err
	}
	original := r.Run(*seed, nil)
	if !original.Failed {
		return errors.New("seed does not fail, nothing to shrink")
	}
	fmt.Printf("seed %d fails with %d faults: %s\nshrinking...\n", *seed, len(original.Faults), original.Reason)
	best := engine.Shrink(r, original, func(n int) { fmt.Printf("  %d faults still fail\n", n) })
	out, err := saveSchedule(*conf, *seed, best.Faults)
	if err != nil {
		return err
	}
	fmt.Println()
	report(best, 40)
	fmt.Printf("\nminimal schedule: %s\nreplay: seedfall replay -c %s -seed %d -faults %s\n", out, *conf, *seed, out)
	return nil
}
