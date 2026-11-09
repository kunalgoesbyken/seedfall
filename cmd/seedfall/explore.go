package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultExploreSeeds    = "1-32"
	defaultExploreBatch    = 16
	defaultExplorePatience = 8
	defaultSweepBatch      = 20
)

type exploreArgs struct {
	configArgs
	Seeds        string `json:"seeds,omitempty" jsonschema:"seed or inclusive range run as the first round, default 1-32"`
	Rounds       int    `json:"rounds,omitempty" jsonschema:"stop after this many rounds; default no limit"`
	Batch        int    `json:"batch,omitempty" jsonschema:"child runs per round, default 16"`
	UntilNovelty int    `json:"until_novelty,omitempty" jsonschema:"stop after this many rounds in a row add nothing new, default 8"`
	Budget       string `json:"budget,omitempty" jsonschema:"stop after this wall-clock time, for example 10m; default none"`
	ExploreSeed  uint64 `json:"explore_seed,omitempty" jsonschema:"seed of the exploration's own choices, default 1"`
	KeepGoing    bool   `json:"keep_going,omitempty" jsonschema:"keep exploring after the first failure"`
	Corpus       string `json:"corpus,omitempty" jsonschema:"corpus directory, default .seedfall/corpus/<config>"`
}

type exploreView struct {
	Runs           int                  `json:"runs"`
	Failed         int                  `json:"failed"`
	Signals        int                  `json:"signals"`
	Functions      int                  `json:"functions_reached"`
	Corpus         int                  `json:"corpus_entries"`
	Loaded         int                  `json:"corpus_loaded"`
	Stale          int                  `json:"corpus_stale"`
	Dir            string               `json:"corpus_dir"`
	Stopped        string               `json:"stopped"`
	SeedsPerSecond float64              `json:"seeds_per_second"`
	VirtualPerWall float64              `json:"virtual_seconds_per_wall_second"`
	Rounds         []engine.GrowthPoint `json:"rounds"`
	Properties     []propertyView       `json:"properties"`
	Failure        *failureView         `json:"failure,omitempty"`
}

func cmdExplore(args []string) error {
	fs := newFlags("explore")
	var a exploreArgs
	a.register(fs)
	fs.StringVar(&a.Seeds, "seeds", defaultExploreSeeds, "seed or inclusive seed range run as the first round")
	fs.IntVar(&a.Rounds, "rounds", 0, "stop after this many rounds (default no limit)")
	fs.IntVar(&a.Batch, "batch", defaultExploreBatch, "child runs per round")
	fs.IntVar(&a.UntilNovelty, "until-novelty", defaultExplorePatience, "stop after this many rounds in a row add nothing new; 0 keeps going until -rounds or -budget")
	fs.StringVar(&a.Budget, "budget", "", "stop after this wall-clock time, for example 10m")
	fs.Uint64Var(&a.ExploreSeed, "explore-seed", 1, "seed of the exploration's own choices")
	fs.BoolVar(&a.KeepGoing, "keep-going", false, "keep exploring after the first failure")
	fs.StringVar(&a.Corpus, "corpus", "", "corpus directory (default .seedfall/corpus/<config>)")
	jobs := fs.Int("j", runtime.NumCPU(), "parallel runs; the exploration is the same at any value")
	jsonOut := fs.Bool("json", false, "print one JSON object per round, then a summary")
	reportPath := fs.String("report", "", "write an HTML triage report (plus .json) to this path")
	cpuProfile := fs.String("cpuprofile", "", "write a pprof CPU profile of the exploration to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jsonOut && *reportPath != "" {
		return errors.New("-json and -report cannot be combined")
	}
	stop, err := startProfile(*cpuProfile)
	if err != nil {
		return err
	}
	defer stop()
	enc := json.NewEncoder(os.Stdout)
	var encodeErr error
	r, cfg, out, err := runExplore(a, *jobs, func(p engine.GrowthPoint) {
		if *jsonOut {
			encodeErr = cmp.Or(encodeErr, enc.Encode(struct {
				Type string `json:"type"`
				engine.GrowthPoint
			}{"round", p}))
			return
		}
		printRound(p)
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		encodeErr = cmp.Or(encodeErr, enc.Encode(struct {
			Type string `json:"type"`
			exploreView
		}{"summary", viewExplore(r, a.ref().String(), out)}))
		if encodeErr != nil {
			return encodeErr
		}
		exitOnFailures(out.Summary)
		return nil
	}
	if *reportPath != "" {
		return writeReport(r, cfg, a.ref().String(), a.Seeds, *reportPath, out.Summary, nil, out.Elapsed, false)
	}
	printExplore(r, a.ref().String(), out)
	exitOnFailures(out.Summary)
	return nil
}

func exitOnFailures(sum engine.Summary) {
	if len(sum.Failures) > 0 {
		exit(1)
	}
}

func runExplore(a exploreArgs, jobs int, onRound func(engine.GrowthPoint)) (*engine.Runner, *engine.Config, engine.Exploration, error) {
	if err := checkJobs(jobs); err != nil {
		return nil, nil, engine.Exploration{}, err
	}
	if err := nonNegative(namedInt{"-rounds", a.Rounds}, namedInt{"-batch", a.Batch}); err != nil {
		return nil, nil, engine.Exploration{}, err
	}
	lo, hi, err := engine.ParseSeeds(a.Seeds)
	if err != nil {
		return nil, nil, engine.Exploration{}, err
	}
	var budget time.Duration
	if a.Budget != "" {
		if budget, err = time.ParseDuration(a.Budget); err != nil {
			return nil, nil, engine.Exploration{}, fmt.Errorf("budget: %w", err)
		}
	}
	r, cfg, err := loadWith(a.ref(), true)
	if err != nil {
		return nil, nil, engine.Exploration{}, err
	}
	out, err := engine.Explore(r, engine.ExploreOptions{
		Lo: lo, Hi: hi, Rounds: a.Rounds, Batch: a.Batch, UntilNovelty: a.UntilNovelty, Budget: budget, Jobs: jobs,
		Seed: a.ExploreSeed, KeepGoing: a.KeepGoing, Corpus: a.Corpus, OnRound: onRound,
	})
	return r, cfg, out, err
}

func printRound(p engine.GrowthPoint) {
	fmt.Printf("round %-3d runs %-5d +%d signals (%d)  +%d functions (%d)  corpus %d  failures %d  %s\n", p.Round, p.Runs, p.NewSignals, p.Signals, p.NewFunctions, p.Functions, p.Corpus, p.Failures, p.Elapsed.Round(time.Millisecond))
	for _, s := range p.Sample {
		fmt.Printf("    new: %s\n", s)
	}
}

func printExplore(r *engine.Runner, conf string, out engine.Exploration) {
	fmt.Printf("\nstopped: %s\n", out.Stopped)
	fmt.Printf("corpus: %d entries in %s", out.Corpus, filepath.ToSlash(out.Dir))
	if out.Loaded > 0 || out.Stale > 0 {
		fmt.Printf(" (%d loaded from earlier runs, %d stale and skipped)", out.Loaded, out.Stale)
	}
	fmt.Println()
	printEfficiency(out.Summary)
	if out.First != nil {
		printFailure(*out.First, 25)
		s := out.First.Schedule(engine.Schedule{})
		a, _, err := r.SaveArtifacts(out.First.Seed, s, exploreArtifact(conf, out.First))
		printArtifacts(a, err)
		file := filepath.ToSlash(filepath.Join(a.Dir, engine.ArtifactFaults))
		fmt.Printf("replay: %s\nshrink: seedfall shrink -c %s -seed %d -faults %s\n", a.Replay, conf, out.First.Seed, file)
		return
	}
	printCoverage(out.Coverage)
	if unmet := out.Coverage.Unmet(); len(unmet) > 0 {
		fmt.Printf("note: %d properties were never reached or never true in %d runs\n", len(unmet), out.Runs())
	}
	fmt.Printf("ok  %d runs, %d passed\n", out.Runs(), out.Passed)
}

func exploreArtifact(conf string, res *engine.Result) engine.ArtifactOptions {
	return engine.ArtifactOptions{Config: conf, Original: res, Label: "explore-" + engine.ScheduleID(res.Seed, res.Schedule(engine.Schedule{}))}
}

func viewExplore(r *engine.Runner, conf string, out engine.Exploration) exploreView {
	v := exploreView{
		Runs: out.Runs(), Failed: len(out.Failures), Corpus: out.Corpus, Loaded: out.Loaded, Stale: out.Stale, Dir: filepath.ToSlash(out.Dir), Stopped: out.Stopped,
		SeedsPerSecond: out.SeedsPerSecond(), VirtualPerWall: out.VirtualPerWall(), Rounds: out.Growth, Properties: properties(out.Coverage),
	}
	if n := len(out.Growth); n > 0 {
		v.Signals, v.Functions = out.Growth[n-1].Signals, out.Growth[n-1].Functions
	}
	if out.First != nil {
		v.Failure = viewFailure(conf, *out.First)
		s := out.First.Schedule(engine.Schedule{})
		opts := exploreArtifact(conf, out.First)
		withArtifacts(r, v.Failure, s, opts)
		v.Failure.Replay = fmt.Sprintf("seedfall replay -c %s -seed %d -faults %s", conf, out.First.Seed, filepath.ToSlash(filepath.Join(v.Failure.Artifacts, engine.ArtifactFaults)))
	}
	return v
}

func toolExplore(_ context.Context, _ *mcp.CallToolRequest, a exploreArgs) (*mcp.CallToolResult, exploreView, error) {
	if a.Seeds == "" {
		a.Seeds = defaultExploreSeeds
	}
	if a.Batch == 0 {
		a.Batch = defaultExploreBatch
	}
	if a.UntilNovelty == 0 {
		a.UntilNovelty = defaultExplorePatience
	}
	if a.ExploreSeed == 0 {
		a.ExploreSeed = 1
	}
	r, _, out, err := runExplore(a, runtime.NumCPU(), nil)
	if err != nil {
		return nil, exploreView{}, err
	}
	return nil, viewExplore(r, a.ref().String(), out), nil
}

var exitHook = func() {}

func exit(code int) {
	exitHook()
	os.Exit(code)
}

func startProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("-cpuprofile does not work on Windows: Go's profiler crashes unwinding through wazero's compiled guest code")
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return nil, err
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			pprof.StopCPUProfile()
			f.Close()
		}
	}
	exitHook = stop
	return stop, nil
}

func printEfficiency(sum engine.Summary) {
	if sum.Elapsed > 0 {
		fmt.Printf("efficiency: %.1f runs/s, %.0fx virtual time per wall second\n", sum.SeedsPerSecond(), sum.VirtualPerWall())
	}
	if u := sum.Utilization(); u != nil {
		fmt.Printf("utilization: %s\n", u.Advice)
	}
}

func printGrowth(sum engine.Summary) {
	if len(sum.Growth) > 0 {
		fmt.Println("round  runs  new signals  signals  new functions  functions")
	}
	for _, p := range sum.Growth {
		fmt.Printf("  %-5d %-5d %-12d %-8d %-14d %d\n", p.Round, p.Runs, p.NewSignals, p.Signals, p.NewFunctions, p.Functions)
	}
	if sum.Stopped != "" {
		fmt.Printf("stopped: %s\n\n", sum.Stopped)
	}
}
