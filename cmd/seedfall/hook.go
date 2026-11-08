package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const blockStop = 2

func cmdHook([]string) error {
	seeds := os.Getenv("SEEDFALL_HOOK_SEEDS")
	var in struct {
		StopHookActive bool   `json:"stop_hook_active"`
		Cwd            string `json:"cwd"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return fmt.Errorf("hook: read the Claude Code hook input from stdin: %w", err)
	}
	if seeds == "" || in.StopHookActive {
		return nil
	}
	config := filepath.Join(in.Cwd, cmp.Or(os.Getenv("SEEDFALL_HOOK_CONFIG"), "seedfall.json"))
	if _, err := os.Stat(config); err != nil {
		return nil
	}
	lo, hi, err := engine.ParseSeeds(seeds)
	if err != nil {
		return err
	}
	r, cfg, err := load(config)
	if err != nil {
		return err
	}
	regs, err := replayRegressions(r, cfg, true, "", runtime.NumCPU())
	if err != nil {
		return err
	}
	if bad := engine.FirstFailed(regs); bad != nil {
		fmt.Fprintf(os.Stderr, "seedfall: pinned regression %s fails again under %s: %s\nreplay: seedfall replay -c %s -seed %d -faults %s\n", bad.String(), config, bad.Result.Reason, config, bad.File.Seed, bad.Path)
		os.Exit(blockStop)
	}
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: runtime.NumCPU()})
	if sum.First != nil {
		res := *sum.First
		fmt.Fprintf(os.Stderr, "seedfall: seed %d of %s fails under %s: %s\nreplay: seedfall replay -c %s -seed %d\nshrink: seedfall shrink -c %s -seed %d\n", res.Seed, seeds, config, res.Reason, config, res.Seed, config, res.Seed)
		os.Exit(blockStop)
	}
	if unmet := sum.Coverage.Unmet(); len(unmet) > 0 {
		fmt.Fprintf(os.Stderr, "seedfall: %d properties were never reached or never true in seeds %s of %s; run seedfall run -c %s -seeds %s for the table\n", len(unmet), seeds, config, config, seeds)
		os.Exit(blockStop)
	}
	return nil
}
