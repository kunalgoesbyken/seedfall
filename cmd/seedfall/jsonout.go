package main

import (
	"cmp"
	"encoding/json"
	"os"

	"github.com/kunalgoesbyken/seedfall/engine"
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
}

type summaryLine struct {
	Type       string         `json:"type"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Unfinished int            `json:"unfinished"`
	Unmet      int            `json:"unmet_properties"`
	Properties []propertyView `json:"properties"`
	Buggify    []buggifyView  `json:"buggify_sites,omitempty"`
}

func runJSON(r *engine.Runner, lo, hi uint64, jobs int) error {
	enc := json.NewEncoder(os.Stdout)
	var encodeErr error
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: jobs, KeepGoing: true, OnResult: func(res engine.Result) {
		encodeErr = cmp.Or(encodeErr, enc.Encode(seedJSON(res)))
	}})
	if encodeErr != nil {
		return encodeErr
	}
	return printSummaryJSON(enc, sum)
}

func seedJSON(res engine.Result) seedLine {
	line := seedLine{
		Type: "seed", Seed: res.Seed, Passed: !res.Failed, Finished: res.Finished, Reason: res.Reason,
		VirtualSeconds: res.Duration.Seconds(), Faults: len(res.FaultsApplied()),
	}
	if res.Failed {
		line.Failure = &res.Failure
	}
	return line
}

func printSummaryJSON(enc *json.Encoder, sum engine.Summary) error {
	unmet := len(sum.Coverage.Unmet())
	line := summaryLine{Type: "summary", Passed: sum.Passed, Failed: len(sum.Failures), Unfinished: sum.Unfinished, Unmet: unmet, Properties: properties(sum.Coverage), Buggify: buggifySites(sum.Buggify)}
	if err := enc.Encode(line); err != nil {
		return err
	}
	if line.Failed > 0 || unmet > 0 {
		os.Exit(1)
	}
	return nil
}
