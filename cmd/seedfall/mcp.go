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

	Class *engine.Classification `json:"classification,omitempty"`
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

type causeArgs struct {
	Config   string `json:"config" jsonschema:"path to seedfall.json"`
	Seed     uint64 `json:"seed" jsonschema:"failing seed to explain"`
	Points   int    `json:"points,omitempty" jsonschema:"evenly spaced moments to branch from, default 16"`
	Branches int    `json:"branches,omitempty" jsonschema:"alternate futures per moment, default 20"`
}

type causePointView struct {
	At          string   `json:"at"`
	Probability float64  `json:"failure_probability"`
	After       []string `json:"after_faults,omitempty"`
}

type causeView struct {
	Failure *failureView     `json:"failure"`
	Curve   []causePointView `json:"curve"`
}

type inspectArgs struct {
	Config string `json:"config" jsonschema:"path to seedfall.json"`
	Seed   uint64 `json:"seed" jsonschema:"seed to replay"`
	Faults string `json:"faults,omitempty" jsonschema:"optional fault schedule file written by seedfall_shrink"`
	At     string `json:"at,omitempty" jsonschema:"virtual time to snapshot at, for example 1.5s; default is the moment of failure"`
	Node   string `json:"node,omitempty" jsonschema:"only inspect this node"`
	Path   string `json:"path,omitempty" jsonschema:"also return the durable content of this file, as text or a hex dump"`
}

type fileView struct {
	Path        string `json:"path"`
	DurableSize int64  `json:"durable_size"`
	Encoding    string `json:"encoding"`
	Content     string `json:"content"`
}

type inspectView struct {
	Seed    uint64        `json:"seed"`
	Passed  bool          `json:"passed"`
	At      string        `json:"snapshot_at"`
	Failure *failureView  `json:"failure,omitempty"`
	Disks   []engine.Disk `json:"disks"`
	File    *fileView     `json:"file,omitempty"`
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
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_cause",
		Description: "Explain a failing seed: replay it up to many moments, re-randomize everything after each one, and return the probability of failing from each moment. The fault where the probability jumps is the cause.",
	}, toolCause)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_verify",
		Description: "Prove a fix: replay the failing seeds and schedules (given directly or taken from a triage report), sweep enough fresh seeds for 95% confidence, check determinism and optionally re-run the durability mutants. Returns the verdict and a Markdown proof block for the pull request.",
	}, toolVerify)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_inspect",
		Description: "Replay a seed and snapshot every node's simulated disk at the failure or at a virtual time. Returns each file's size, durable size, the byte ranges a crash would lose, directory entries that are not durable yet, removed entries a crash would bring back, and the last torn write. Optionally returns one file's durable content.",
	}, toolInspect)
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
		class := r.Classify(*sum.First, nil)
		out.Failure.Class = &class
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
		class := r.Classify(res, faults)
		out.Failure.Class = &class
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

func toolCause(_ context.Context, _ *mcp.CallToolRequest, a causeArgs) (*mcp.CallToolResult, causeView, error) {
	r, _, err := load(a.Config)
	if err != nil {
		return nil, causeView{}, err
	}
	opts := engine.CauseOptions{Points: cmp.Or(a.Points, defaultPoints), Branches: cmp.Or(a.Branches, defaultBranches), Jobs: runtime.NumCPU()}
	original, curve, err := engine.Cause(r, a.Seed, opts)
	if err != nil {
		return nil, causeView{}, err
	}
	out := causeView{Failure: viewFailure(a.Config, original)}
	for _, p := range curve {
		view := causePointView{At: p.At.String(), Probability: p.Probability()}
		for _, f := range p.Faults {
			view.After = append(view.After, f.Describe())
		}
		out.Curve = append(out.Curve, view)
	}
	return nil, out, nil
}

func toolInspect(_ context.Context, _ *mcp.CallToolRequest, a inspectArgs) (*mcp.CallToolResult, inspectView, error) {
	at, err := parseAt(a.At)
	if err != nil {
		return nil, inspectView{}, err
	}
	r, _, err := load(a.Config)
	if err != nil {
		return nil, inspectView{}, err
	}
	faults, err := readFaults(a.Faults)
	if err != nil {
		return nil, inspectView{}, err
	}
	in, err := r.Inspect(a.Seed, faults, engine.InspectOptions{At: at, Node: a.Node})
	if err != nil {
		return nil, inspectView{}, err
	}
	out := inspectView{Seed: a.Seed, Passed: !in.Result.Failed, At: in.At.String(), Disks: in.Disks}
	if in.Result.Failed {
		out.Failure = viewFailure(a.Config, in.Result)
	}
	if a.Path == "" {
		return nil, out, nil
	}
	f, err := durableFile(in.Disks, a.Path)
	if err != nil {
		return nil, inspectView{}, err
	}
	encoding, content := renderContent(f.Durable)
	out.File = &fileView{Path: f.Path, DurableSize: f.DurableSize, Encoding: encoding, Content: content}
	return nil, out, nil
}

func parseAt(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
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

func schedulePath(config string, seed uint64) string {
	return filepath.Join(filepath.Dir(config), ".seedfall", fmt.Sprintf("seed-%d.faults.json", seed))
}

func saveSchedule(config string, seed uint64, faults []engine.Fault) (string, error) {
	path := schedulePath(config, seed)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
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
