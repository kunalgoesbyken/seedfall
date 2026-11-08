package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type shrinkArgs struct {
	Config string `json:"config" jsonschema:"path to seedfall.json"`
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

type scheduleFile struct {
	Seed uint64 `json:"seed"`
	engine.Schedule
	ShrunkFrom      *engine.ScheduleSize `json:"shrunk_from,omitempty"`
	ShrinkRuns      int                  `json:"shrink_runs,omitempty"`
	BudgetExhausted bool                 `json:"budget_exhausted,omitempty"`
}

func cmdShrink(args []string) error {
	fs := flag.NewFlagSet("shrink", flag.ExitOnError)
	conf := fs.String("c", "seedfall.json", "config file")
	seed := seedFlag(fs)
	jobs := fs.Int("j", runtime.NumCPU(), "candidate schedules run at once; the result is the same for any value")
	budget := fs.String("budget", "", "stop after this many runs (300) or this much wall-clock time (10m) and keep the smallest schedule so far")
	fs.Parse(args)
	opts, err := parseBudget(*budget)
	if err != nil {
		return err
	}
	opts.Jobs = *jobs
	r, _, err := load(*conf)
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
	out, err := saveSchedule(*conf, *seed, shrunk)
	if err != nil {
		return err
	}
	fmt.Println()
	printFailure(shrunk.Result, 40)
	fmt.Printf("\nshrunk from %s\n         to %s\nin %d runs, %s\n", shrunk.Before, shrunk.After, shrunk.Runs, time.Since(started).Round(time.Millisecond))
	if shrunk.Exhausted {
		fmt.Println("budget spent: the schedule fails the same way but may not be minimal; run shrink again with a larger -budget")
	}
	label := "minimal schedule"
	if shrunk.Exhausted {
		label = "shrunk schedule"
	}
	fmt.Printf("%s: %s\nreplay: seedfall replay -c %s -seed %d -faults %s\n", label, out, *conf, *seed, out)
	return nil
}

func toolShrink(_ context.Context, _ *mcp.CallToolRequest, a shrinkArgs) (*mcp.CallToolResult, shrinkView, error) {
	opts, err := parseBudget(a.Budget)
	if err != nil {
		return nil, shrinkView{}, err
	}
	opts.Jobs = runtime.NumCPU()
	r, _, err := load(a.Config)
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
	failure := viewFailure(a.Config, shrunk.Result)
	failure.Replay = fmt.Sprintf("seedfall replay -c %s -seed %d -faults %s", a.Config, a.Seed, path)
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
	b, err := os.ReadFile(path)
	if err != nil {
		return engine.Schedule{}, err
	}
	var file scheduleFile
	if trimmed := bytes.TrimSpace(b); len(trimmed) > 0 && trimmed[0] == '[' {
		err = json.Unmarshal(trimmed, &file.Faults)
	} else {
		err = json.Unmarshal(b, &file)
	}
	if err != nil {
		return engine.Schedule{}, fmt.Errorf("%s: %w", path, err)
	}
	if file.Faults == nil {
		file.Faults = []engine.Fault{}
	}
	return file.Schedule, nil
}

func schedulePath(config string, seed uint64) string {
	return filepath.Join(filepath.Dir(config), ".seedfall", fmt.Sprintf("seed-%d.faults.json", seed))
}

func saveSchedule(config string, seed uint64, shrunk engine.Shrunk) (string, error) {
	path := schedulePath(config, seed)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	file := scheduleFile{Seed: seed, Schedule: shrunk.Schedule, ShrunkFrom: &shrunk.Before, ShrinkRuns: shrunk.Runs, BudgetExhausted: shrunk.Exhausted}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o644)
}
