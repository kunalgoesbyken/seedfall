package report

import (
	"cmp"
	"fmt"
	"math"
	"runtime"
	"slices"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const (
	confidence   = 0.05
	exampleSeeds = 10
	exampleTrace = 40
	rareRate     = 0.01
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
	Seed   uint64   `json:"seed"`
	At     string   `json:"at"`
	Reason string   `json:"reason"`
	Faults []string `json:"faults_before_failure"`
	Trace  []string `json:"trace_tail"`
	Replay string   `json:"replay"`
	Shrink string   `json:"shrink"`
}

type Finding struct {
	Key     string         `json:"key"`
	Failure engine.Failure `json:"failure"`
	Status  Status         `json:"status"`
	Count   int            `json:"failing_seeds"`
	Rate    float64        `json:"rate"`
	Rare    bool           `json:"rare"`
	Seeds   []uint64       `json:"example_seeds"`
	Example *Example       `json:"example,omitempty"`
	Note    string         `json:"note,omitempty"`
}

type Property struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	Location     string `json:"location,omitempty"`
	SeedsReached int    `json:"seeds_reached"`
	SeedsTrue    int    `json:"seeds_true"`
	Problem      string `json:"problem,omitempty"`
}

type Report struct {
	Config     string           `json:"config"`
	Seeds      string           `json:"seeds"`
	Total      int              `json:"seeds_run"`
	Passed     int              `json:"seeds_passed"`
	Failed     int              `json:"seeds_failed"`
	Elapsed    string           `json:"elapsed"`
	Generated  time.Time        `json:"generated"`
	GoVersion  string           `json:"go_version"`
	Faults     engine.FaultSpec `json:"faults"`
	Findings   []Finding        `json:"findings"`
	Cleared    []Finding        `json:"previous_findings_not_seen"`
	Properties []Property       `json:"properties"`
}

type Input struct {
	Config  string
	Seeds   string
	Faults  engine.FaultSpec
	Summary engine.Summary
	Elapsed time.Duration
	Replay  func(seed uint64) engine.Result
}

func Build(in Input, history map[string]known) Report {
	total := in.Summary.Passed + len(in.Summary.Failures)
	r := Report{
		Config: in.Config, Seeds: in.Seeds, Total: total, Passed: in.Summary.Passed, Failed: len(in.Summary.Failures),
		Elapsed: in.Elapsed.Round(time.Millisecond).String(), Generated: time.Now(), GoVersion: runtime.Version(), Faults: in.Faults,
		Properties: properties(in.Summary.Coverage),
	}
	for _, group := range groupFailures(in.Summary.Failures) {
		r.Findings = append(r.Findings, finding(in, group, total, history))
	}
	r.Cleared = cleared(r.Findings, history, total)
	return r
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
	f.Rare = f.Rate < rareRate
	if k, ok := history[f.Key]; ok {
		f.Status = StatusOngoing
		if k.resolved(0) {
			f.Status = StatusRegressed
		}
	}
	for _, res := range group[:min(len(group), exampleSeeds)] {
		f.Seeds = append(f.Seeds, res.Seed)
	}
	f.Example = example(in, in.Replay(first.Seed))
	return f
}

func example(in Input, res engine.Result) *Example {
	e := &Example{
		Seed: res.Seed, At: res.At.String(), Reason: res.Reason,
		Replay: fmt.Sprintf("seedfall replay -c %s -seed %d", in.Config, res.Seed),
		Shrink: fmt.Sprintf("seedfall shrink -c %s -seed %d", in.Config, res.Seed),
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
	var out []Finding
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

func seedsToConfirm(failureRate float64) int {
	if failureRate >= 1 {
		return 1
	}
	return int(math.Ceil(math.Log(confidence) / math.Log(1-failureRate)))
}

func properties(c engine.Coverage) []Property {
	var out []Property
	for _, msg := range c.Messages() {
		p := c[msg]
		out = append(out, Property{Kind: p.Kind.String(), Message: msg, Location: p.Location, SeedsReached: p.SeedsReached, SeedsTrue: p.SeedsTrue, Problem: p.Problem()})
	}
	return out
}

func (r Report) entry() Entry {
	e := Entry{Time: r.Generated, Config: r.Config, Seeds: r.Total, Findings: map[string]int{}}
	for _, f := range r.Findings {
		e.Findings[f.Key] = f.Count
	}
	return e
}
