package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pinArgs struct {
	configArgs
	Seed   uint64 `json:"seed" jsonschema:"seed to pin"`
	Faults string `json:"faults,omitempty" jsonschema:"fault schedule to pin; default is the schedule seedfall_shrink saved for the seed, or else the seed's own faults"`
}

type pinView struct {
	File    string       `json:"pinned_file"`
	From    string       `json:"from"`
	Faults  int          `json:"faults"`
	Passes  bool         `json:"passes_now"`
	Failure *failureView `json:"failure,omitempty"`
}

func cmdPin(args []string) error {
	fs := newFlags("pin")
	ca := configFlags(fs)
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "schedule to pin (default: .seedfall/seed-N.faults.json from shrink if present, else the seed's own faults)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := pin(pinArgs{configArgs: *ca, Seed: *seed, Faults: *faultsFile})
	if err != nil {
		return err
	}
	fmt.Printf("pinned seed %d (%d faults, from %s) to %s\n", *seed, out.Faults, out.From, out.File)
	if out.Passes {
		fmt.Println("it passes now; seedfall run, verify and seedfalltest replay it first and fail if it fails again")
		return nil
	}
	fmt.Printf("it fails now: %s\nseedfall run, verify and seedfalltest will fail on it until the bug is fixed\n", out.Failure.Reason)
	return nil
}

func toolPin(_ context.Context, _ *mcp.CallToolRequest, a pinArgs) (*mcp.CallToolResult, pinView, error) {
	out, err := pin(a)
	return nil, out, err
}

func pin(a pinArgs) (pinView, error) {
	r, cfg, err := load(a.ref())
	if err != nil {
		return pinView{}, err
	}
	file, from, err := scheduleSource(a.Config, a.Seed, a.Faults, func() (engine.Schedule, error) {
		return r.Run(a.Seed, nil).Schedule(engine.Schedule{}), nil
	})
	if err != nil {
		return pinView{}, err
	}
	file.Duration, file.ShrunkFrom, file.ShrinkRuns, file.BudgetExhausted = 0, nil, 0, false
	res := r.Replay(a.Seed, file.Schedule)
	file.Pinned = engine.NewPin(cfg)
	file.Pinned.From, file.Pinned.Version, file.Pinned.Date = from, engine.Version(), time.Now().UTC()
	out := pinView{File: engine.PinPath(cfg, a.Seed), From: from, Faults: len(file.Faults), Passes: !res.Failed}
	if res.Failed {
		file.Pinned.Finding, file.Pinned.Reason = res.Failure.Key(), res.Reason
		out.Failure = viewFailure(a.ref().String(), res)
		out.Failure.Replay = fmt.Sprintf("seedfall replay -c %s -seed %d -faults %s", a.ref().String(), a.Seed, filepath.ToSlash(out.File))
	}
	return out, engine.WriteScheduleFile(out.File, file)
}

func scheduleSource(config string, seed uint64, path string, own func() (engine.Schedule, error)) (engine.ScheduleFile, string, error) {
	if path == "" {
		path = schedulePath(config, seed)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			s, err := own()
			return engine.ScheduleFile{Seed: seed, Schedule: s}, fmt.Sprintf("seed %d's own faults", seed), err
		}
	}
	file, err := engine.ReadScheduleFile(path)
	if err != nil {
		return file, "", err
	}
	if file.Seed != 0 && file.Seed != seed {
		return file, "", fmt.Errorf("%s is a schedule for seed %d, not %d", path, file.Seed, seed)
	}
	file.Seed = seed
	return file, filepath.ToSlash(path), nil
}

func replayRegressions(r *engine.Runner, cfg *engine.Config, pinned bool, seedsFile string, jobs int) ([]engine.RegressionResult, error) {
	var regs []engine.Regression
	if pinned {
		found, err := engine.Pinned(cfg)
		if err != nil {
			return nil, err
		}
		regs = append(regs, found...)
	}
	if seedsFile != "" {
		found, err := engine.SeedsFile(seedsFile)
		if err != nil {
			return nil, err
		}
		regs = append(regs, found...)
	}
	return engine.ReplayRegressions(r, regs, jobs), nil
}

func regressionArtifacts(r *engine.Runner, config string, reg engine.RegressionResult) (engine.Artifact, error) {
	a, _, err := r.SaveArtifacts(reg.File.Seed, reg.Schedule(), reg.ArtifactOptions(config, &reg.Result))
	return a, err
}

func viewRegressions(r *engine.Runner, config string, results []engine.RegressionResult) []report.Regression {
	out := make([]report.Regression, 0, len(results))
	for _, reg := range results {
		v := report.Regression{Source: filepath.ToSlash(reg.Source), Seed: reg.File.Seed, Passed: !reg.Result.Failed}
		if reg.Result.Failed {
			v.Failure, v.Reason = &reg.Result.Failure, reg.Result.Reason
			a, err := regressionArtifacts(r, config, reg)
			if a.Classification.Verdict != "" {
				v.Class = &a.Classification
			}
			if err == nil {
				v.Artifacts = filepath.ToSlash(a.Dir)
			}
		}
		out = append(out, v)
	}
	return out
}

func printRegressions(results []engine.RegressionResult) {
	if len(results) == 0 {
		return
	}
	fmt.Printf("regressions, replayed before the sweep (%d):\n", len(results))
	for _, reg := range results {
		mark := "pass"
		if reg.Result.Failed {
			mark = "FAIL"
		}
		fmt.Printf("  %s  %s\n", mark, reg.String())
	}
	fmt.Println()
}
