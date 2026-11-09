package report

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"path"
	"runtime"
	"slices"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const (
	confidence   = 0.05
	exampleSeeds = 10
	exampleTrace = 40
	RareRate     = 0.01
)

type Status string

const (
	StatusNew         Status = "new"
	StatusOngoing     Status = "ongoing"
	StatusRegressed   Status = "regressed"
	StatusResolved    Status = "resolved"
	StatusUnconfirmed Status = "not seen"
)

type Example struct {
	Seed      uint64   `json:"seed"`
	At        string   `json:"at"`
	Reason    string   `json:"reason"`
	Faults    []string `json:"faults_before_failure"`
	Trace     []string `json:"trace_tail"`
	Replay    string   `json:"replay"`
	Shrink    string   `json:"shrink"`
	Artifacts string   `json:"artifacts,omitempty"`
	Schedule  string   `json:"schedule_file,omitempty"`
	Timeline  string   `json:"timeline,omitempty"`
}

type Finding struct {
	Key            string                 `json:"key"`
	Failure        engine.Failure         `json:"failure"`
	Classification *engine.Classification `json:"classification,omitempty"`
	Status         Status                 `json:"status"`
	Count          int                    `json:"failing_seeds"`
	Rate           float64                `json:"rate"`
	Rare           bool                   `json:"rare"`
	Seeds          []uint64               `json:"example_seeds"`
	Example        *Example               `json:"example,omitempty"`
	Note           string                 `json:"note,omitempty"`
}

type Property struct {
	Kind         string   `json:"kind"`
	Message      string   `json:"message"`
	Location     string   `json:"location,omitempty"`
	SeedsReached int      `json:"seeds_reached"`
	SeedsTrue    int      `json:"seeds_true"`
	Problem      string   `json:"problem,omitempty"`
	Margin       *float64 `json:"margin,omitempty"`
	MarginNote   string   `json:"-"`
}

type Regression struct {
	Source    string                 `json:"source"`
	Seed      uint64                 `json:"seed"`
	Passed    bool                   `json:"passed"`
	Failure   *engine.Failure        `json:"failure,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
	Class     *engine.Classification `json:"classification,omitempty"`
	Artifacts string                 `json:"artifacts,omitempty"`
}

type Report struct {
	Config         string               `json:"config"`
	History        string               `json:"history,omitempty"`
	Seeds          string               `json:"seeds"`
	Total          int                  `json:"seeds_run"`
	Passed         int                  `json:"seeds_passed"`
	Failed         int                  `json:"seeds_failed"`
	Elapsed        string               `json:"elapsed"`
	Generated      time.Time            `json:"generated"`
	GoVersion      string               `json:"go_version"`
	Version        string               `json:"seedfall_version"`
	Commit         string               `json:"commit,omitempty"`
	Faults         engine.FaultSpec     `json:"faults"`
	Regressions    []Regression         `json:"regressions,omitempty"`
	Findings       []Finding            `json:"findings"`
	Cleared        []Finding            `json:"previous_findings_not_seen"`
	Properties     []Property           `json:"properties"`
	Exploration    []engine.GrowthPoint `json:"exploration,omitempty"`
	Stopped        string               `json:"stopped,omitempty"`
	SeedsPerSec    float64              `json:"seeds_per_second"`
	VirtualPerWall float64              `json:"virtual_seconds_per_wall_second"`
	Utilization    *engine.Utilization  `json:"utilization,omitempty"`
}

type Replayed struct {
	Result    engine.Result
	Class     engine.Classification
	Artifacts string
}

type Input struct {
	Config      string
	Seeds       string
	Version     string
	Commit      string
	Faults      engine.FaultSpec
	Summary     engine.Summary
	Regressions []Regression
	Elapsed     time.Duration
	Replay      func(failed engine.Result) Replayed
}

func Build(in Input, history map[string]known) (Report, error) {
	if in.Replay == nil {
		return Report{}, errors.New("report: Input.Replay is required")
	}
	total := in.Summary.Passed + len(in.Summary.Failures)
	r := Report{
		Config: in.Config, Seeds: in.Seeds, Total: total, Passed: in.Summary.Passed, Failed: len(in.Summary.Failures),
		Elapsed: in.Elapsed.Round(time.Millisecond).String(), Generated: time.Now(), GoVersion: runtime.Version(), Version: in.Version, Commit: in.Commit,
		Faults: in.Faults, Regressions: in.Regressions, Findings: []Finding{}, Properties: properties(in.Summary.Coverage),
		Exploration: in.Summary.Growth, Stopped: in.Summary.Stopped, SeedsPerSec: in.Summary.SeedsPerSecond(), VirtualPerWall: in.Summary.VirtualPerWall(),
		Utilization: in.Summary.Utilization(),
	}
	for _, group := range groupFailures(in.Summary.Failures) {
		r.Findings = append(r.Findings, finding(in, group, total, history))
	}
	r.Cleared = cleared(r.Findings, history, total)
	return r, nil
}

func groupFailures(failures []engine.Result) [][]engine.Result {
	byKey := map[string][]engine.Result{}
	var keys []string
	for _, f := range failures {
		key := f.Failure.Key()
		if _, ok := byKey[key]; !ok {
			keys = append(keys, key)
		}
		byKey[key] = append(byKey[key], f)
	}
	groups := make([][]engine.Result, 0, len(keys))
	for _, k := range keys {
		g := byKey[k]
		slices.SortFunc(g, func(a, b engine.Result) int { return cmp.Compare(a.Seed, b.Seed) })
		groups = append(groups, g)
	}
	slices.SortFunc(groups, func(a, b []engine.Result) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a[0].Seed, b[0].Seed))
	})
	return groups
}

func finding(in Input, group []engine.Result, total int, history map[string]known) Finding {
	first := group[0]
	f := Finding{
		Key: first.Failure.Key(), Failure: first.Failure, Count: len(group),
		Rate: float64(len(group)) / float64(total), Status: StatusNew,
	}
	f.Rare = f.Rate < RareRate
	if k, ok := history[f.Key]; ok {
		f.Status = StatusOngoing
		if k.resolved(0) {
			f.Status = StatusRegressed
		}
	}
	for _, res := range group[:min(len(group), exampleSeeds)] {
		f.Seeds = append(f.Seeds, res.Seed)
	}
	replayed := in.Replay(first)
	f.Example = example(in, first, replayed)
	f.Classification = &replayed.Class
	return f
}

func example(in Input, first engine.Result, replayed Replayed) *Example {
	res := replayed.Result
	e := &Example{
		Seed: res.Seed, At: res.At.String(), Reason: res.Reason, Artifacts: replayed.Artifacts,
		Replay: fmt.Sprintf("seedfall replay -c %s -seed %d", in.Config, res.Seed),
		Shrink: fmt.Sprintf("seedfall shrink -c %s -seed %d", in.Config, res.Seed),
	}
	if first.Scheduled {
		faults := path.Join(replayed.Artifacts, engine.ArtifactFaults)
		e.Replay, e.Shrink, e.Schedule = e.Replay+" -faults "+faults, e.Shrink+" -faults "+faults, faults
	}
	for _, fault := range res.FaultsBefore() {
		e.Faults = append(e.Faults, fault.Describe())
	}
	e.Trace = res.Trace[max(0, len(res.Trace)-exampleTrace):]
	return e
}

func cleared(current []Finding, history map[string]known, total int) []Finding {
	seen := map[string]bool{}
	for _, f := range current {
		seen[f.Key] = true
	}
	out := []Finding{}
	for key, k := range history {
		if seen[key] || k.resolved(0) {
			continue
		}
		clean := k.cleanSeeds + total
		f := Finding{Key: key, Status: StatusResolved, Note: fmt.Sprintf("not seen in %d seeds since it failed %d of %d", clean, k.failures, k.seeds)}
		if !k.resolved(total) {
			f.Status = StatusUnconfirmed
			f.Note = fmt.Sprintf("not seen in %d seeds, but %d are needed to be 95%% sure it is fixed", clean, k.seedsToConfirm())
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b Finding) int { return cmp.Compare(a.Key, b.Key) })
	return out
}

func SeedsToConfirm(failureRate float64) int {
	if failureRate >= 1 {
		return 1
	}
	return int(math.Ceil(math.Log(confidence) / math.Log(1-failureRate)))
}

func DetectableRate(cleanSeeds int) float64 {
	return 1 - math.Pow(confidence, 1/float64(cleanSeeds))
}

func properties(c engine.Coverage) []Property {
	out := []Property{}
	for _, msg := range c.Messages() {
		p := c[msg]
		out = append(out, Property{Kind: p.Kind.String(), Message: msg, Location: p.Location, SeedsReached: p.SeedsReached, SeedsTrue: p.SeedsTrue, Problem: p.Problem(), Margin: p.Margin, MarginNote: p.Kind.MarginNote(p.Margin)})
	}
	return out
}

func (r Report) entry() Entry {
	e := Entry{Time: r.Generated, Config: r.Config, Seeds: r.Total, Version: r.Version, Findings: map[string]int{}}
	for _, f := range r.Findings {
		e.Findings[f.Key] = f.Count
	}
	return e
}

func (r Report) FailedRegressions() []Regression {
	var out []Regression
	for _, reg := range r.Regressions {
		if !reg.Passed {
			out = append(out, reg)
		}
	}
	return out
}

func (r Report) RegressionsFailed() int { return len(r.FailedRegressions()) }

func (r Report) UnmetProperties() []Property {
	var out []Property
	for _, p := range r.Properties {
		if p.Problem != "" {
			out = append(out, p)
		}
	}
	return out
}
