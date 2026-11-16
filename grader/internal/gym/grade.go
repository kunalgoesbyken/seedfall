package gym

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const (
	ClassProgress   = "progress"
	ClassUnfinished = "unfinished"
	traceTailLines  = 40
	determinismRuns = 2
	buildLogLimit   = 1 << 16
)

type Options struct {
	Task        Task
	Workspace   string
	SeedfallSrc string
	Seeds       []uint64
	Explicit    bool
	Jobs        int
}

type SeedReport struct {
	Seed      uint64   `json:"seed"`
	Passed    bool     `json:"passed"`
	Class     string   `json:"class,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Hash      string   `json:"hash"`
	Acks      int      `json:"acks"`
	Faults    []string `json:"faults,omitempty"`
	TraceTail []string `json:"trace_tail,omitempty"`
}

type HarnessGuard struct {
	Restored bool     `json:"restored"`
	Ignored  []string `json:"ignored_files,omitempty"`
}

type ProgressGuard struct {
	MinAcks int  `json:"min_acks"`
	Starved int  `json:"starved_seeds"`
	Tripped bool `json:"tripped"`
}

type DeterminismGuard struct {
	Seeds   []uint64 `json:"seeds"`
	Tripped bool     `json:"tripped"`
}

type Guard struct {
	Tripped     bool             `json:"tripped"`
	Harness     HarnessGuard     `json:"harness"`
	Progress    ProgressGuard    `json:"progress"`
	Determinism DeterminismGuard `json:"determinism"`
}

type Report struct {
	Task         string       `json:"task"`
	N            int          `json:"n"`
	Total        int          `json:"total"`
	Passed       int          `json:"passed"`
	Failed       int          `json:"failed"`
	BuildOK      bool         `json:"build_ok"`
	BuildError   string       `json:"build_error,omitempty"`
	Reward       float64      `json:"reward"`
	PerSeed      []SeedReport `json:"per_seed"`
	Guard        Guard        `json:"guard"`
	BuildSeconds float64      `json:"build_seconds"`
	RunSeconds   float64      `json:"run_seconds"`
}

func Grade(o Options) (Report, error) {
	rep := Report{Task: o.Task.ID, N: len(o.Seeds), Total: len(o.Seeds), PerSeed: []SeedReport{}}
	rep.Guard.Harness = HarnessGuard{Restored: true, Ignored: ignoredFiles(o.Task, o.Workspace)}
	baseline, err := o.Task.Baseline()
	if err != nil {
		return rep, err
	}
	dir, cleanup, err := Stage(o.Task, o.Workspace, o.SeedfallSrc)
	if err != nil {
		return rep, err
	}
	defer cleanup()
	started := time.Now()
	runner, workload, buildErr, err := build(dir)
	rep.BuildSeconds = time.Since(started).Seconds()
	if err != nil {
		return rep, err
	}
	if buildErr != "" {
		rep.BuildError = buildErr
		return rep, nil
	}
	defer runner.Close()
	rep.BuildOK = true
	started = time.Now()
	results := runAll(runner, o.Seeds, o.Jobs)
	rep.RunSeconds = time.Since(started).Seconds()
	for _, res := range results {
		rep.PerSeed = append(rep.PerSeed, seedReport(res, workload, baseline.MinAcks, o.Explicit))
	}
	rep.Guard.Progress = progressGuard(rep.PerSeed, baseline.MinAcks)
	rep.Guard.Determinism = determinismGuard(runner, results)
	rep.finish()
	return rep, nil
}

func (r *Report) finish() {
	for _, s := range r.PerSeed {
		if s.Passed {
			r.Passed++
		}
	}
	r.Failed = r.N - r.Passed
	r.Guard.Tripped = r.Guard.Progress.Tripped || r.Guard.Determinism.Tripped
	if r.BuildOK && !r.Guard.Tripped && r.N > 0 {
		r.Reward = float64(r.Passed) / float64(r.N)
	}
}

func build(dir string) (*engine.Runner, string, string, error) {
	cfg, err := engine.Load(engine.ConfigRef{Path: filepath.Join(dir, ConfigFile)})
	if err != nil {
		return nil, "", "", err
	}
	log, err := os.CreateTemp("", "gym-build-")
	if err != nil {
		return nil, "", "", err
	}
	defer os.Remove(log.Name())
	defer log.Close()
	stderr := os.Stderr
	os.Stderr = log
	buildErr := engine.Build(cfg)
	os.Stderr = stderr
	if buildErr != nil {
		text, _ := io.ReadAll(io.NewSectionReader(log, 0, buildLogLimit))
		return nil, "", strings.TrimSpace(string(text)) + "\n" + buildErr.Error(), nil
	}
	runner, err := engine.NewRunner(cfg)
	if err != nil {
		return nil, "", err.Error(), nil
	}
	return runner, cfg.Workload.Name, "", nil
}

func runAll(r *engine.Runner, seeds []uint64, jobs int) []engine.Result {
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	results := make([]engine.Result, len(seeds))
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(jobs, len(seeds)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = r.Run(seeds[i], nil)
			}
		}()
	}
	for i := range seeds {
		next <- i
	}
	close(next)
	wg.Wait()
	return results
}

func seedReport(res engine.Result, workload string, minAcks int, explicit bool) SeedReport {
	s := SeedReport{Seed: res.Seed, Hash: res.Hash(), Acks: acks(res, workload)}
	switch {
	case res.Failed:
		s.Class, s.Reason = string(res.Failure.Class), res.Reason
	case !res.Finished:
		s.Class, s.Reason = ClassUnfinished, "the workload did not finish within the simulated duration"
	case s.Acks < minAcks:
		s.Class, s.Reason = ClassProgress, fmt.Sprintf("the workload saw %d acknowledged operations, fewer than the %d a working server reaches", s.Acks, minAcks)
	default:
		s.Passed = true
	}
	if explicit && !s.Passed {
		s.Faults = describeFaults(res)
		s.TraceTail = res.Trace[max(0, len(res.Trace)-traceTailLines):]
	}
	return s
}

func describeFaults(res engine.Result) []string {
	var out []string
	for _, f := range res.FaultsApplied() {
		out = append(out, fmt.Sprintf("%s: %s", time.Duration(f.At), f.Describe()))
	}
	return out
}

func acks(res engine.Result, workload string) int {
	n := 0
	for _, e := range res.Events {
		if e.Name == AckEvent && strings.HasPrefix(e.Instance, workload) {
			n++
		}
	}
	return n
}

func progressGuard(seeds []SeedReport, minAcks int) ProgressGuard {
	g := ProgressGuard{MinAcks: minAcks}
	for _, s := range seeds {
		if s.Acks < minAcks {
			g.Starved++
		}
	}
	g.Tripped = g.Starved*2 > len(seeds)
	return g
}

func determinismGuard(r *engine.Runner, results []engine.Result) DeterminismGuard {
	g := DeterminismGuard{}
	for _, res := range results[:min(determinismRuns, len(results))] {
		g.Seeds = append(g.Seeds, res.Seed)
		again := r.Run(res.Seed, nil)
		if again.Hash() != res.Hash() || again.Failed != res.Failed {
			g.Tripped = true
		}
	}
	return g
}

func ignoredFiles(task Task, workspace string) []string {
	var ignored []string
	filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		rel = filepath.ToSlash(rel)
		if !slices.ContainsFunc(task.Editable, func(e string) bool { return strings.HasPrefix(rel, strings.TrimSuffix(filepath.ToSlash(e), "/")+"/") }) {
			ignored = append(ignored, rel)
		}
		return nil
	})
	return ignored
}
