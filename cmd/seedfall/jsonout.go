package main

import (
	"cmp"
	"encoding/json"
	"os"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
)

type seedLine struct {
	Type           string          `json:"type"`
	Seed           uint64          `json:"seed"`
	Passed         bool            `json:"passed"`
	Finished       bool            `json:"finished"`
	Failure        *engine.Failure `json:"failure,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	VirtualSeconds float64         `json:"virtual_seconds"`
	Faults         int             `json:"faults"`
	HostCalls      int64           `json:"host_calls"`
	Hash           string          `json:"determinism_hash"`
}

type regressionLine struct {
	Type string `json:"type"`
	report.Regression
}

type summaryLine struct {
	Type        string         `json:"type"`
	Passed      int            `json:"passed"`
	Failed      int            `json:"failed"`
	Unfinished  int            `json:"unfinished"`
	Regressions int            `json:"regressions_failed"`
	Unmet       int            `json:"unmet_properties"`
	Properties  []propertyView `json:"properties"`
	Buggify     []buggifyView  `json:"buggify_sites,omitempty"`

	SeedsPerSecond float64              `json:"seeds_per_second"`
	VirtualPerWall float64              `json:"virtual_seconds_per_wall_second"`
	Stopped        string               `json:"stopped,omitempty"`
	Growth         []engine.GrowthPoint `json:"exploration,omitempty"`
	Utilization    *engine.Utilization  `json:"utilization,omitempty"`
}

func runJSON(r *engine.Runner, config string, regs []engine.RegressionResult, lo, hi uint64, opts engine.SweepOptions) error {
	enc := json.NewEncoder(os.Stdout)
	var encodeErr error
	regressionsFailed := 0
	for _, reg := range viewRegressions(r, config, regs) {
		encodeErr = cmp.Or(encodeErr, enc.Encode(regressionLine{Type: "regression", Regression: reg}))
		if !reg.Passed {
			regressionsFailed++
		}
	}
	opts.KeepGoing = true
	opts.OnResult = func(res engine.Result) {
		encodeErr = cmp.Or(encodeErr, enc.Encode(seedJSON(res)))
	}
	sum := engine.Sweep(r, lo, hi, opts)
	if encodeErr != nil {
		return encodeErr
	}
	return printSummaryJSON(enc, sum, regressionsFailed)
}

func seedJSON(res engine.Result) seedLine {
	line := seedLine{
		Type: "seed", Seed: res.Seed, Passed: !res.Failed, Finished: res.Finished, Reason: res.Reason,
		VirtualSeconds: res.Duration.Seconds(), Faults: len(res.FaultsApplied()), HostCalls: res.HostCalls, Hash: res.Hash(),
	}
	if res.Failed {
		line.Failure = &res.Failure
	}
	return line
}

func printSummaryJSON(enc *json.Encoder, sum engine.Summary, regressionsFailed int) error {
	unmet := len(sum.Coverage.Unmet())
	line := summaryLine{Type: "summary", Passed: sum.Passed, Failed: len(sum.Failures), Unfinished: sum.Unfinished, Regressions: regressionsFailed, Unmet: unmet, Properties: properties(sum.Coverage), Buggify: buggifySites(sum.Buggify), SeedsPerSecond: sum.SeedsPerSecond(), VirtualPerWall: sum.VirtualPerWall(), Stopped: sum.Stopped, Growth: sum.Growth, Utilization: sum.Utilization()}
	if err := enc.Encode(line); err != nil {
		return err
	}
	if line.Failed > 0 || regressionsFailed > 0 || unmet > 0 {
		exit(1)
	}
	return nil
}
