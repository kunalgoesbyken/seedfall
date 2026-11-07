package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpTraceTail = 60

type runArgs struct {
	Config string `json:"config" jsonschema:"path to seedfall.json"`
	Seeds  string `json:"seeds" jsonschema:"seed or inclusive range, for example 1-200"`
	Check  bool   `json:"check,omitempty" jsonschema:"run every seed twice and fail if the traces differ"`
}

type seedArgs struct {
	Config string `json:"config" jsonschema:"path to seedfall.json"`
	Seed   uint64 `json:"seed" jsonschema:"seed to run"`
	Faults string `json:"faults,omitempty" jsonschema:"optional fault schedule file written by seedfall_shrink"`
}

type failureView struct {
	Seed   uint64   `json:"seed"`
	At     string   `json:"at"`
	Reason string   `json:"reason"`
	Faults []string `json:"faults_before_failure"`
	Trace  []string `json:"trace_tail"`
	Replay string   `json:"replay_command"`
}

type propertyView struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	Location     string `json:"location,omitempty"`
	Problem      string `json:"problem,omitempty"`
	SeedsReached int    `json:"seeds_reached"`
	SeedsTrue    int    `json:"seeds_true"`
}

type runView struct {
	Passed     int            `json:"seeds_passed"`
	Unfinished int            `json:"seeds_hit_horizon"`
	Failure    *failureView   `json:"failure,omitempty"`
	Properties []propertyView `json:"properties"`
	Elapsed    string         `json:"elapsed"`
}

type seedView struct {
	Seed    uint64       `json:"seed"`
	Passed  bool         `json:"passed"`
	Steps   int          `json:"steps"`
	Failure *failureView `json:"failure,omitempty"`
	Trace   []string     `json:"trace_tail,omitempty"`
}

type shrinkView struct {
	OriginalFaults int          `json:"original_faults"`
	MinimalFaults  int          `json:"minimal_faults"`
	ScheduleFile   string       `json:"schedule_file"`
	Failure        *failureView `json:"failure"`
}

func cmdMCP([]string) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "seedfall", Version: "0.2.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_run",
		Description: "Sweep a seed range under simulated crashes, torn writes, fsync errors and partitions. Returns the lowest failing seed with its fault schedule and trace tail, plus property coverage.",
	}, toolRun)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_replay",
		Description: "Replay one seed exactly, optionally with a fault schedule file, and return its trace.",
	}, toolReplay)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_shrink",
		Description: "Minimize the fault schedule of a failing seed to the faults that still cause the failure, and save it for replay.",
	}, toolShrink)
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

func toolRun(_ context.Context, _ *mcp.CallToolRequest, a runArgs) (*mcp.CallToolResult, runView, error) {
	lo, hi, err := engine.ParseSeeds(a.Seeds)
	if err != nil {
		return nil, runView{}, err
	}
	r, _, err := load(a.Config)
	if err != nil {
		return nil, runView{}, err
	}
	r.Doublecheck = a.Check
	started := time.Now()
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: runtime.NumCPU()})
	out := runView{Passed: sum.Passed, Unfinished: sum.Unfinished, Properties: properties(sum.Coverage), Elapsed: time.Since(started).Round(time.Millisecond).String()}
	if sum.First != nil {
		out.Failure = viewFailure(a.Config, *sum.First)
	}
	return nil, out, nil
}

func toolReplay(_ context.Context, _ *mcp.CallToolRequest, a seedArgs) (*mcp.CallToolResult, seedView, error) {
	r, _, err := load(a.Config)
	if err != nil {
		return nil, seedView{}, err
	}
	faults, err := readFaults(a.Faults)
	if err != nil {
		return nil, seedView{}, err
	}
	res := r.Run(a.Seed, faults)
	out := seedView{Seed: a.Seed, Passed: !res.Failed, Steps: res.Steps}
	if res.Failed {
		out.Failure = viewFailure(a.Config, res)
		return nil, out, nil
	}
	out.Trace = tail(res.Trace, mcpTraceTail)
	return nil, out, nil
}

func toolShrink(_ context.Context, _ *mcp.CallToolRequest, a seedArgs) (*mcp.CallToolResult, shrinkView, error) {
	r, _, err := load(a.Config)
	if err != nil {
		return nil, shrinkView{}, err
	}
	original := r.Run(a.Seed, nil)
	if !original.Failed {
		return nil, shrinkView{}, errors.New("seed does not fail, nothing to shrink")
	}
	best := engine.Shrink(r, original, func(int) {})
	path, err := saveSchedule(a.Config, a.Seed, best.Faults)
	if err != nil {
		return nil, shrinkView{}, err
	}
	return nil, shrinkView{OriginalFaults: len(original.Faults), MinimalFaults: len(best.Faults), ScheduleFile: path, Failure: viewFailure(a.Config, best)}, nil
}

func readFaults(path string) ([]engine.Fault, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	faults := []engine.Fault{}
	return faults, json.Unmarshal(b, &faults)
}

func saveSchedule(config string, seed uint64, faults []engine.Fault) (string, error) {
	dir := filepath.Join(filepath.Dir(config), ".seedfall")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("seed-%d.faults.json", seed))
	b, err := json.MarshalIndent(faults, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o644)
}

func viewFailure(config string, res engine.Result) *failureView {
	happened := res.FaultsBefore()
	described := make([]string, len(happened))
	for i, f := range happened {
		described[i] = f.Describe()
	}
	return &failureView{
		Seed: res.Seed, At: res.At.String(), Reason: res.Reason,
		Faults: described, Trace: tail(res.Trace, mcpTraceTail),
		Replay: fmt.Sprintf("seedfall replay -c %s -seed %d", config, res.Seed),
	}
}

func properties(c engine.Coverage) []propertyView {
	out := []propertyView{}
	for _, msg := range c.Messages() {
		p := c[msg]
		out = append(out, propertyView{Kind: p.Kind.String(), Message: msg, Location: p.Location, Problem: p.Problem(), SeedsReached: p.SeedsReached, SeedsTrue: p.SeedsTrue})
	}
	return out
}

func tail(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
