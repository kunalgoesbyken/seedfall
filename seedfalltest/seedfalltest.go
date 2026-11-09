package seedfalltest

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const traceTail = 30

func Sweep(t testing.TB, config, seeds string) {
	t.Helper()
	r, cfg := runner(t, config)
	lo, hi, err := engine.ParseSeeds(seeds)
	if err != nil {
		t.Fatal(err)
	}
	replay(t, r, cfg, config, pinned(t, cfg))
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: runtime.NumCPU()})
	if sum.First != nil {
		fail(t, r, config, engine.Regression{File: engine.ScheduleFile{Seed: sum.First.Seed}}, *sum.First)
	}
	if unmet := sum.Coverage.Unmet(); len(unmet) > 0 {
		t.Fatalf("seedfall: properties never reached or never true in %d seeds: %s", sum.Passed, strings.Join(unmet, "; "))
	}
	t.Logf("seedfall: %d seeds passed", sum.Passed)
}

func Pinned(t *testing.T, config string) {
	t.Helper()
	r, cfg := runner(t, config)
	regs := pinned(t, cfg)
	if len(regs) == 0 {
		t.Skipf("seedfall: no schedules pinned in %s for %s", filepath.Join(cfg.Dir, engine.RegressionsDir), config)
	}
	each(t, r, cfg, config, regs)
}

func Seeds(t *testing.T, config string, seeds []uint64) {
	t.Helper()
	r, cfg := runner(t, config)
	regs := make([]engine.Regression, len(seeds))
	for i, seed := range seeds {
		regs[i] = engine.Regression{Source: fmt.Sprintf("seed %d", seed), File: engine.ScheduleFile{Seed: seed}}
	}
	each(t, r, cfg, config, regs)
}

func Replay(t testing.TB, config, faultsFile string) {
	t.Helper()
	r, cfg := runner(t, config)
	file, err := engine.ReadScheduleFile(faultsFile)
	if err != nil {
		t.Fatal(err)
	}
	replay(t, r, cfg, config, []engine.Regression{{Source: faultsFile, Path: faultsFile, Label: "schedule", File: file}})
}

func runner(t testing.TB, config string) (*engine.Runner, *engine.Config) {
	t.Helper()
	cfg, err := engine.LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Build(cfg); err != nil {
		t.Fatal(err)
	}
	r, err := engine.NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, cfg
}

func pinned(t testing.TB, cfg *engine.Config) []engine.Regression {
	t.Helper()
	regs, err := engine.Pinned(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return regs
}

func each(t *testing.T, r *engine.Runner, cfg *engine.Config, config string, regs []engine.Regression) {
	t.Helper()
	for _, reg := range regs {
		t.Run(filepath.Base(reg.Source), func(t *testing.T) { replay(t, r, cfg, config, []engine.Regression{reg}) })
	}
}

func replay(t testing.TB, r *engine.Runner, cfg *engine.Config, config string, regs []engine.Regression) {
	t.Helper()
	if bad := engine.FirstFailed(engine.ReplayRegressions(r, regs, runtime.NumCPU())); bad != nil {
		fail(t, r, config, bad.Regression, bad.Result)
	}
}

func fail(t testing.TB, r *engine.Runner, config string, reg engine.Regression, res engine.Result) {
	t.Helper()
	where := fmt.Sprintf("seed %d", res.Seed)
	if reg.Source != "" {
		where = reg.String()
	}
	trace := res.Trace[max(0, len(res.Trace)-traceTail):]
	msg := fmt.Sprintf("seedfall: %s failed at t=%s: %s\n%s", where, res.At, res.Reason, strings.Join(trace, "\n"))
	a, _, err := r.SaveArtifacts(res.Seed, reg.Schedule(), reg.ArtifactOptions(config, &res))
	if err != nil {
		t.Fatalf("%s\nreplay: seedfall replay -c %s -seed %d\nartifacts: %v", msg, config, res.Seed, err)
	}
	t.Fatalf("%s\nclass: %s\nreplay: %s\nartifacts: %s", msg, a.Classification, a.Replay, a.Dir)
}
