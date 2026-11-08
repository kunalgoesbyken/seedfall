package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type shrinkArgs struct {
	configArgs
	Seed   uint64 `json:"seed" jsonschema:"failing seed to shrink"`
	Budget string `json:"budget,omitempty" jsonschema:"optional limit: a number of runs such as 300, or a wall-clock duration such as 10m; default is no limit"`
}

type shrinkView struct {
	OriginalFaults  int                 `json:"original_faults"`
	MinimalFaults   int                 `json:"minimal_faults"`
	Original        engine.ScheduleSize `json:"original"`
	Minimal         engine.ScheduleSize `json:"minimal"`
	Runs            int                 `json:"runs"`
	BudgetExhausted bool                `json:"budget_exhausted"`
	ScheduleFile    string              `json:"schedule_file"`
	Failure         *failureView        `json:"failure"`
}

func cmdShrink(args []string) error {
	fs := newFlags("shrink")
	ca := configFlags(fs)
	seed := seedFlag(fs)
	jobs := fs.Int("j", runtime.NumCPU(), "candidate schedules run at once; the result is the same for any value")
	budget := fs.String("budget", "", "stop after this many runs (300) or this much wall-clock time (10m) and keep the smallest schedule so far")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conf := ca.ref().String()
	opts, err := parseBudget(*budget)
	if err != nil {
		return err
	}
	opts.Jobs = *jobs
	r, _, err := load(ca.ref())
	if err != nil {
		return err
	}
	original := r.Run(*seed, nil)
	if !original.Failed {
		return errors.New("seed does not fail, nothing to shrink")
	}
	fmt.Printf("seed %d fails with %d faults: %s\nshrinking with %d jobs%s...\n", *seed, len(original.Faults), original.Reason, opts.Jobs, budgetNote(opts))
	opts.Progress = func(p engine.ShrinkStep) { fmt.Printf("  run %4d  %3d faults  %s\n", p.Runs, p.Faults, p.Change) }
	started := time.Now()
	shrunk := engine.Shrink(r, original, opts)
	out, err := saveSchedule(ca.Config, *seed, shrunk)
	if err != nil {
		return err
	}
	fmt.Println()
	printFailure(shrunk.Result, 40)
	fmt.Printf("\nshrunk from %s\n         to %s\nin %d runs, %s\n", shrunk.Before, shrunk.After, shrunk.Runs, time.Since(started).Round(time.Millisecond))
	if shrunk.Exhausted {
		fmt.Println("budget spent: the schedule fails the same way but may not be minimal; run shrink again with a larger -budget")
	}
	a, _, err := r.SaveArtifacts(*seed, shrunk.Schedule, engine.ArtifactOptions{Config: conf, ScheduleFile: out, Label: "shrunk", Original: &shrunk.Result})
	printArtifacts(a, err)
	label := "minimal schedule"
	if shrunk.Exhausted {
		label = "shrunk schedule"
	}
	fmt.Printf("%s: %s\nreplay: seedfall replay -c %s -seed %d -faults %s\n", label, out, conf, *seed, out)
	return nil
}

func toolShrink(_ context.Context, _ *mcp.CallToolRequest, a shrinkArgs) (*mcp.CallToolResult, shrinkView, error) {
	opts, err := parseBudget(a.Budget)
	if err != nil {
		return nil, shrinkView{}, err
	}
	opts.Jobs = runtime.NumCPU()
	r, _, err := load(a.ref())
	if err != nil {
		return nil, shrinkView{}, err
	}
	original := r.Run(a.Seed, nil)
	if !original.Failed {
		return nil, shrinkView{}, errors.New("seed does not fail, nothing to shrink")
	}
	shrunk := engine.Shrink(r, original, opts)
	path, err := saveSchedule(a.Config, a.Seed, shrunk)
	if err != nil {
		return nil, shrinkView{}, err
	}
	failure := viewFailure(a.ref().String(), shrunk.Result)
	withArtifacts(r, failure, a.ref().String(), shrunk.Schedule, engine.ArtifactOptions{Config: a.ref().String(), ScheduleFile: path, Label: "shrunk", Original: &shrunk.Result})
	failure.Replay = fmt.Sprintf("seedfall replay -c %s -seed %d -faults %s", a.ref().String(), a.Seed, path)
	return nil, shrinkView{
		OriginalFaults: shrunk.Before.Faults, MinimalFaults: shrunk.After.Faults,
		Original: shrunk.Before, Minimal: shrunk.After, Runs: shrunk.Runs, BudgetExhausted: shrunk.Exhausted,
		ScheduleFile: path, Failure: failure,
	}, nil
}

func cutShort(res engine.Result, cfg *engine.Config) string {
	if res.Failed || res.Finished || res.Horizon >= time.Duration(cfg.Duration) {
		return ""
	}
	return fmt.Sprintf("the schedule's duration of %s ended the run before the workload finished (the config runs for %s), so this pass does not show a fix; seedfall verify replays a schedule for the config's full duration", res.Horizon, time.Duration(cfg.Duration))
}

func parseBudget(s string) (engine.ShrinkOptions, error) {
	if s == "" {
		return engine.ShrinkOptions{}, nil
	}
	if runs, err := strconv.Atoi(s); err == nil {
		if runs <= 0 {
			return engine.ShrinkOptions{}, fmt.Errorf("budget %q must be a positive number of runs", s)
		}
		return engine.ShrinkOptions{MaxRuns: runs}, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return engine.ShrinkOptions{}, fmt.Errorf("budget %q is neither a number of runs nor a positive duration", s)
	}
	return engine.ShrinkOptions{MaxTime: d}, nil
}

func budgetNote(opts engine.ShrinkOptions) string {
	switch {
	case opts.MaxRuns > 0:
		return fmt.Sprintf(", budget %d runs", opts.MaxRuns)
	case opts.MaxTime > 0:
		return fmt.Sprintf(", budget %s", opts.MaxTime)
	}
	return ""
}

func readSchedule(path string) (engine.Schedule, error) {
	if path == "" {
		return engine.Schedule{}, nil
	}
	file, err := engine.ReadScheduleFile(path)
	return file.Schedule, err
}

func schedulePath(config string, seed uint64) string {
	return filepath.Join(filepath.Dir(config), ".seedfall", fmt.Sprintf("seed-%d.faults.json", seed))
}

func saveSchedule(config string, seed uint64, shrunk engine.Shrunk) (string, error) {
	path := schedulePath(config, seed)
	file := engine.ScheduleFile{Seed: seed, Schedule: shrunk.Schedule, ShrunkFrom: &shrunk.Before, ShrinkRuns: shrunk.Runs, BudgetExhausted: shrunk.Exhausted}
	return path, engine.WriteScheduleFile(path, file)
}
