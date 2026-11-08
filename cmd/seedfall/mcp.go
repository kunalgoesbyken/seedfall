package main

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpTraceTail    = 60
	defaultLogLimit = 200
)

const mcpInstructions = "seedfall runs Go services as wasip1 guests under simulated crashes, torn writes, fsync errors, disk corruption, full disks, partitions, clogs and slow nodes, with exact replay from a seed. In a module without a seedfall.json, call seedfall_init first. Read seedfall://docs/properties before writing checks and seedfall://schema/config before editing seedfall.json. The prompts seedfall-setup, seedfall-properties, seedfall-triage and seedfall-fix-and-prove walk through each stage."

type runArgs struct {
	configArgs
	Seeds      string `json:"seeds" jsonschema:"seed or inclusive range, for example 1-200"`
	Check      bool   `json:"check,omitempty" jsonschema:"run every seed twice and fail at the first host call or trace line that differs"`
	SkipPinned bool   `json:"skip_pinned,omitempty" jsonschema:"do not replay the schedules pinned in seedfall-regressions/ before the sweep"`
}

type seedArgs struct {
	configArgs
	Seed   uint64 `json:"seed" jsonschema:"seed to run"`
	Faults string `json:"faults,omitempty" jsonschema:"optional fault schedule file written by seedfall_shrink"`
}

type failureView struct {
	Seed      uint64   `json:"seed"`
	At        string   `json:"at"`
	Reason    string   `json:"reason"`
	Faults    []string `json:"faults_before_failure"`
	Trace     []string `json:"trace_tail"`
	Replay    string   `json:"replay_command"`
	History   string   `json:"history_file,omitempty"`
	Timeline  string   `json:"timeline_file,omitempty"`
	Artifacts string   `json:"artifacts_dir,omitempty"`

	Class *engine.Classification `json:"classification,omitempty"`
}

type checkArgs struct {
	History string `json:"history" jsonschema:"history file, for example .seedfall/seed-4.history.jsonl"`
	Model   string `json:"model,omitempty" jsonschema:"model to check against, default kv"`
}

type propertyView struct {
	Kind         string   `json:"kind"`
	Message      string   `json:"message"`
	Location     string   `json:"location,omitempty"`
	Problem      string   `json:"problem,omitempty"`
	SeedsReached int      `json:"seeds_reached"`
	SeedsTrue    int      `json:"seeds_true"`
	Margin       *float64 `json:"margin,omitempty" jsonschema:"numeric assertions only: the smallest margin seen for always, the largest for sometimes, where margin is left minus right for GreaterThan and right minus left for LessThan"`
}

type buggifyView struct {
	Site         string `json:"site"`
	Location     string `json:"location,omitempty"`
	Problem      string `json:"problem,omitempty"`
	SeedsReached int    `json:"seeds_reached"`
	SeedsEnabled int    `json:"seeds_enabled"`
	SeedsFired   int    `json:"seeds_fired"`
	Fired        int    `json:"fired"`
}

type runView struct {
	Regressions []report.Regression `json:"regressions,omitempty"`
	Passed      int                 `json:"seeds_passed"`
	Unfinished  int                 `json:"seeds_hit_horizon"`
	Failure     *failureView        `json:"failure,omitempty"`
	Properties  []propertyView      `json:"properties"`
	Buggify     []buggifyView       `json:"buggify_sites,omitempty"`
	Elapsed     string              `json:"elapsed"`
}

type seedView struct {
	Seed    uint64       `json:"seed"`
	Passed  bool         `json:"passed"`
	Steps   int          `json:"steps"`
	Hash    string       `json:"determinism_hash"`
	Failure *failureView `json:"failure,omitempty"`
	Trace   []string     `json:"trace_tail,omitempty"`
	Events  []eventView  `json:"events,omitempty"`
	Note    string       `json:"note,omitempty"`
}

type eventView struct {
	At       string `json:"at"`
	Instance string `json:"instance"`
	Name     string `json:"name"`
	Details  string `json:"details,omitempty"`
}

func events(res engine.Result) []eventView {
	var out []eventView
	for _, e := range res.Events {
		out = append(out, eventView{At: e.At.String(), Instance: e.Instance, Name: e.Name, Details: e.Details})
	}
	return out
}

type causeArgs struct {
	configArgs
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
	configArgs
	Seed   uint64 `json:"seed" jsonschema:"seed to replay"`
	Faults string `json:"faults,omitempty" jsonschema:"optional fault schedule file written by seedfall_shrink"`
	At     string `json:"at,omitempty" jsonschema:"virtual time to snapshot at, for example 1.5s; default is the moment of failure"`
	Node   string `json:"node,omitempty" jsonschema:"only inspect this node"`
	Path   string `json:"path,omitempty" jsonschema:"also return the durable content of this file, as text or a hex dump"`
}

type logsArgs struct {
	seedArgs
	Node     string `json:"node,omitempty" jsonschema:"only lines of these nodes, comma separated: their output, starts, crashes and faults"`
	Grep     string `json:"grep,omitempty" jsonschema:"only lines matching this regular expression"`
	From     string `json:"from,omitempty" jsonschema:"only lines at or after this virtual time, for example 1.5s"`
	To       string `json:"to,omitempty" jsonschema:"only lines at or before this virtual time"`
	NetTrace bool   `json:"net_trace,omitempty" jsonschema:"also trace every connection, message sent and message read"`
	Limit    int    `json:"limit,omitempty" jsonschema:"most lines to return, the first ones that match, default 200"`
}

type logsView struct {
	Seed    uint64    `json:"seed"`
	Passed  bool      `json:"passed"`
	Reason  string    `json:"reason,omitempty"`
	Total   int       `json:"trace_lines"`
	Matched int       `json:"matched"`
	Lines   []logView `json:"lines"`
}

type debugArgs struct {
	seedArgs
	At       string   `json:"at,omitempty" jsonschema:"virtual time to stop at, for example 1.5s: the session opens before the first step after it; default 0"`
	Commands []string `json:"commands,omitempty" jsonschema:"debugger commands to run in order, for example [\"net\", \"ls server\", \"until fault crash\", \"cat server data/kv.log\"]; help lists them; default where"`
	NetTrace bool     `json:"net_trace,omitempty" jsonschema:"also trace every connection, message sent and message read"`
}

type debugOutput struct {
	Command string `json:"command"`
	Output  string `json:"output"`
}

type debugView struct {
	Seed    uint64            `json:"seed"`
	Outputs []debugOutput     `json:"outputs"`
	State   engine.DebugState `json:"state"`
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

func cmdMCP(args []string) error {
	if err := newFlags("mcp").Parse(args); err != nil {
		return err
	}
	server, err := newMCPServer()
	if err != nil {
		return err
	}
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

func newMCPServer() (*mcp.Server, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "seedfall", Version: "0.2.0"}, &mcp.ServerOptions{Instructions: mcpInstructions})
	initOutput, err := jsonschema.For[initView](schemaOptions())
	if err != nil {
		return nil, err
	}
	editInput, err := jsonschema.For[faultsEditArgs](schemaOptions())
	if err != nil {
		return nil, err
	}
	editInput.Properties["add"].Items.Required = []string{"at", "kind"}
	for _, kind := range engine.FaultKinds {
		editInput.Properties["add"].Items.Properties["kind"].Enum = append(editInput.Properties["add"].Items.Properties["kind"].Enum, kind)
	}
	editOutput, err := jsonschema.For[faultsView](schemaOptions())
	if err != nil {
		return nil, err
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:         "seedfall_init",
		Description:  "Onboard a Go module: find its main packages, report what blocks a GOOS=wasip1 build (compile errors from go list, and cgo packages) with the import chain to each and the shim that covers it, list the dependencies compat_overlay would shim, and scaffold a starter seedfall.json and workload skeleton. Existing files are kept. Returns the report and the next steps.",
		OutputSchema: initOutput,
	}, toolInit)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_run",
		Description: "Sweep a seed range under simulated crashes, torn writes, fsync errors, disk corruption, full disks, partitions, clogs and slow nodes. First replays every schedule pinned in seedfall-regressions/ and stops at the first one that fails. Returns the regressions, the lowest failing seed with its fault schedule, trace tail, classification and artifacts_dir (report.json, trace.txt, a replayable faults.json, history and disk images), plus property coverage and, with faults.buggify on, how often each sim.Buggify site was reached, enabled and fired.",
	}, toolRun)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_replay",
		Description: "Replay one seed exactly, optionally with a fault schedule file, and return its trace.",
	}, toolReplay)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_logs",
		Description: "Replay one seed and return its trace lines filtered by node, regular expression and virtual time window: guest stdout and stderr with virtual timestamps, starts, crashes, faults and, with net_trace, every connection, message sent and message read. A guest line that is a JSON object, such as slog's JSON handler output, comes back parsed in fields.",
	}, toolLogs)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_trace_search",
		Description: "Replay one seed and search its trace with a regular expression, optionally only on some nodes and inside a virtual time window. Returns each match with the trace lines of every node around it (context, default 3), each line with its number, virtual time, node and instance, overlapping windows merged, plus how many lines matched.",
	}, toolTraceSearch)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_debug",
		Description: "Time-travel debugger: replay one seed, stop before the first step after a virtual time, and run debugger commands there: where (node states), log, grep, ls, stat and cat on a node's live disk, net (in-flight messages), faults (active and next), step, until <regex>, to <time> and branch (re-randomize the future, as seedfall_cause does). Returns each command's output and the final state.",
	}, toolDebug)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_shrink",
		Description: "Minimize a failing seed while it keeps failing the same way: remove faults by delta debugging, then shorten crash downtimes, partitions, pauses, clogs and slowdowns, shrink clock jumps, narrow partition sides, move faults earlier, trim the run duration and, for workloads sized with sim.Budget, the operation count. Saves the schedule for replay and returns the sizes before and after, the runs used and whether the optional budget ran out.",
	}, toolShrink)
	mcp.AddTool(server, &mcp.Tool{
		Name:         "seedfall_faults_edit",
		Description:  "Edit a seed's fault schedule to test a hypothesis: remove faults by position, shift a timed fault by a duration or a disk-op, read, buggify or preempt fault by a number of operations, and add faults in the faults JSON format. Starts from the given file, else .seedfall/seed-N.faults.json, else the seed's own faults. Checks every fault against the config (known kind and nodes, until not before at, a timed fault not after the end of the run, required fields per kind) and writes nothing if one is wrong. With no edits it lists and checks, and writes only out, or the seed's own faults to .seedfall/seed-N.faults.json so there is a file to edit. Returns the numbered schedule, the file written and the replay command.",
		InputSchema:  editInput,
		OutputSchema: editOutput,
	}, toolFaultsEdit)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_cause",
		Description: "Explain a failing seed: replay it up to many moments, re-randomize everything after each one, and return the probability of failing from each moment. The fault where the probability jumps is the cause.",
	}, toolCause)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_verify",
		Description: "Prove a fix: replay the failing seeds and schedules (given directly or taken from a triage report), sweep enough fresh seeds for 95% confidence, check determinism and optionally re-run the mutants. Returns the verdict and a Markdown proof block for the pull request.",
	}, toolVerify)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_check",
		Description: "Re-check a recorded operation history (the history_file of a failure, or .seedfall/seed-N.history.jsonl written by replay) against a model, one of " + strings.Join(engine.Models(), ", ") + ". list-append checks transactions for anomalies (G0, G1a, G1b, G1c, G-single, G2), every other model checks linearizability. Returns the verdict, each anomaly with its dependency cycle, and on a linearizability violation an HTML timeline file.",
	}, toolCheck)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_inspect",
		Description: "Replay a seed and snapshot every node's simulated disk at the failure or at a virtual time. Returns each file's size, durable size, the byte ranges a crash would lose, directory entries that are not durable yet, removed entries a crash would bring back, the last torn write, under reorder_writes the fs_model and which pending writes the last crash kept, the ranges disk faults corrupted, and with disk_bytes how full each disk is. Optionally returns one file's durable content.",
	}, toolInspect)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_mutate",
		Description: "Grade the checks: sweep the seed range once unmutated (it must pass), then once per mutant, each of which silently breaks one guarantee under the nodes: fsync, directory fsync, O_SYNC, fsync data integrity, rename atomicity, durable truncation, data before a close, no duplicated TCP writes, and a monotonic clock. With app, rebuilds the node packages once per source mutant instead (flipped comparisons, skipped early error returns, skipped Sync calls), picked from the first seed. Returns each mutant as killed (with the seed and reason), survived (a blind spot in the checks), not exercised or did not build.",
	}, toolMutate)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_pin",
		Description: "Pin a seed's fault schedule as a permanent regression: copy the schedule seedfall_shrink saved for it (or a given schedule, or else the seed's own faults) into seedfall-regressions/ next to the config, with the finding, seedfall version and date. seedfall_run, seedfall_verify, seedfall run and seedfalltest replay every pinned schedule before the sweep and fail if one fails. Returns the pinned file and whether the schedule passes now.",
	}, toolPin)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "seedfall_findings",
		Description: "Read a triage report written by seedfall run -report (default .seedfall/report.html) without running anything, optionally only findings with some statuses: every finding with its status (new, ongoing, regressed), failing seed count, rate, classification and an example failure with replay and shrink commands, findings not seen this time (resolved or not seen), properties that were never reached or never true, and the run history of the same config.",
	}, toolFindings)
	if err := addResources(server); err != nil {
		return nil, err
	}
	return server, addPrompts(server)
}

func toolRun(_ context.Context, _ *mcp.CallToolRequest, a runArgs) (*mcp.CallToolResult, runView, error) {
	lo, hi, err := engine.ParseSeeds(a.Seeds)
	if err != nil {
		return nil, runView{}, err
	}
	r, cfg, err := load(a.ref())
	if err != nil {
		return nil, runView{}, err
	}
	r.Doublecheck = a.Check
	started := time.Now()
	regs, err := replayRegressions(r, cfg, !a.SkipPinned, "", runtime.NumCPU())
	if err != nil {
		return nil, runView{}, err
	}
	out := runView{Regressions: viewRegressions(r, a.ref().String(), regs)}
	if bad := engine.FirstFailed(regs); bad != nil {
		out.Failure = viewFailure(a.ref().String(), bad.Result)
		if bad.Path != "" {
			out.Failure.Replay += " -faults " + filepath.ToSlash(bad.Path)
		}
		for _, reg := range out.Regressions {
			if !reg.Passed {
				out.Failure.Class, out.Failure.Artifacts = reg.Class, reg.Artifacts
				break
			}
		}
		out.Elapsed = time.Since(started).Round(time.Millisecond).String()
		return nil, out, nil
	}
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: runtime.NumCPU()})
	out.Passed, out.Unfinished, out.Properties, out.Buggify = sum.Passed, sum.Unfinished, properties(sum.Coverage), buggifySites(sum.Buggify)
	out.Elapsed = time.Since(started).Round(time.Millisecond).String()
	if sum.First != nil {
		out.Failure = viewFailure(a.ref().String(), *sum.First)
		withArtifacts(r, out.Failure, a.ref().String(), engine.Schedule{}, engine.ArtifactOptions{Config: a.ref().String(), Original: sum.First})
	}
	return nil, out, nil
}

func toolReplay(_ context.Context, _ *mcp.CallToolRequest, a seedArgs) (*mcp.CallToolResult, seedView, error) {
	r, cfg, err := load(a.ref())
	if err != nil {
		return nil, seedView{}, err
	}
	sched, err := readSchedule(a.Faults)
	if err != nil {
		return nil, seedView{}, err
	}
	res := r.Replay(a.Seed, sched)
	out := seedView{Seed: a.Seed, Passed: !res.Failed, Steps: res.Steps, Hash: res.Hash(), Events: events(res)}
	if res.Failed {
		out.Failure = viewFailure(a.ref().String(), res)
		opts := engine.ArtifactOptions{Config: a.ref().String(), ScheduleFile: a.Faults, Original: &res}
		if a.Faults != "" {
			opts.Label = "schedule"
		}
		withArtifacts(r, out.Failure, a.ref().String(), sched, opts)
		return nil, out, nil
	}
	out.Trace = tail(res.Trace, mcpTraceTail)
	out.Note = cutShort(res, cfg)
	return nil, out, nil
}

func toolLogs(_ context.Context, _ *mcp.CallToolRequest, a logsArgs) (*mcp.CallToolResult, logsView, error) {
	if a.Limit < 0 {
		return nil, logsView{}, fmt.Errorf("limit %d is negative", a.Limit)
	}
	filter, err := newLogFilter(a.Node, a.Grep, a.From, a.To)
	if err != nil {
		return nil, logsView{}, err
	}
	res, _, err := recordedRun(&a.configArgs, a.Seed, a.Faults, a.NetTrace)
	if err != nil {
		return nil, logsView{}, err
	}
	if unknown := filter.unknownNodes(res.Timeline.Nodes); unknown != "" {
		return nil, logsView{}, fmt.Errorf("unknown node %q", unknown)
	}
	lines := filter.apply(res.Timeline.Logs)
	out := logsView{Seed: a.Seed, Passed: !res.Failed, Reason: res.Reason, Total: len(res.Timeline.Logs), Matched: len(lines), Lines: []logView{}}
	for _, l := range lines[:min(len(lines), cmp.Or(a.Limit, defaultLogLimit))] {
		out.Lines = append(out.Lines, viewLog(l))
	}
	return nil, out, nil
}

func toolDebug(_ context.Context, _ *mcp.CallToolRequest, a debugArgs) (*mcp.CallToolResult, debugView, error) {
	at, err := parseAt(a.At)
	if err != nil {
		return nil, debugView{}, err
	}
	r, _, err := load(a.ref())
	if err != nil {
		return nil, debugView{}, err
	}
	sched, err := readSchedule(a.Faults)
	if err != nil {
		return nil, debugView{}, err
	}
	r.NetTrace = a.NetTrace
	s, err := r.Debug(a.Seed, sched, at)
	if err != nil {
		return nil, debugView{}, err
	}
	defer s.Close()
	d := newDebugREPL(s)
	out := debugView{Seed: a.Seed}
	commands := a.Commands
	if len(commands) == 0 {
		commands = []string{"where"}
	}
	for _, c := range commands {
		var b strings.Builder
		if d.exec(c, &b) {
			break
		}
		out.Outputs = append(out.Outputs, debugOutput{Command: c, Output: b.String()})
	}
	out.State = s.State()
	return nil, out, nil
}

func toolCause(_ context.Context, _ *mcp.CallToolRequest, a causeArgs) (*mcp.CallToolResult, causeView, error) {
	r, _, err := load(a.ref())
	if err != nil {
		return nil, causeView{}, err
	}
	opts := engine.CauseOptions{Points: cmp.Or(a.Points, defaultPoints), Branches: cmp.Or(a.Branches, defaultBranches), Jobs: runtime.NumCPU()}
	original, curve, err := engine.Cause(r, a.Seed, opts)
	if err != nil {
		return nil, causeView{}, err
	}
	out := causeView{Failure: viewFailure(a.ref().String(), original)}
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
	r, _, err := load(a.ref())
	if err != nil {
		return nil, inspectView{}, err
	}
	sched, err := readSchedule(a.Faults)
	if err != nil {
		return nil, inspectView{}, err
	}
	in, err := r.Inspect(a.Seed, sched, engine.InspectOptions{At: at, Node: a.Node})
	if err != nil {
		return nil, inspectView{}, err
	}
	out := inspectView{Seed: a.Seed, Passed: !in.Result.Failed, At: in.At.String(), Disks: in.Disks}
	if in.Result.Failed {
		out.Failure = viewFailure(a.ref().String(), in.Result)
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

func toolCheck(_ context.Context, _ *mcp.CallToolRequest, a checkArgs) (*mcp.CallToolResult, checkView, error) {
	out, err := checkFile(a.History, cmp.Or(a.Model, engine.DefaultConfig().Checker.Model), "")
	return nil, out, err
}

func parseAt(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func viewFailure(config string, res engine.Result) *failureView {
	happened := res.FaultsBefore()
	described := make([]string, len(happened))
	for i, f := range happened {
		described[i] = f.Describe()
	}
	files := saveHistory(res)
	return &failureView{
		Seed: res.Seed, At: res.At.String(), Reason: res.Reason,
		Faults: described, Trace: tail(res.Trace, mcpTraceTail),
		Replay:  fmt.Sprintf("seedfall replay -c %s -seed %d", config, res.Seed),
		History: files.History, Timeline: files.Timeline,
	}
}

func withArtifacts(r *engine.Runner, view *failureView, config string, s engine.Schedule, opts engine.ArtifactOptions) {
	a, _, err := r.SaveArtifacts(view.Seed, s, opts)
	if a.Classification.Verdict != "" {
		view.Class = &a.Classification
	}
	if err == nil {
		view.Artifacts = filepath.ToSlash(a.Dir)
	}
}

func buggifySites(c engine.BuggifySites) []buggifyView {
	var out []buggifyView
	for _, name := range c.Names() {
		s := c[name]
		out = append(out, buggifyView{Site: name, Location: s.Location, Problem: s.Problem(), SeedsReached: s.SeedsReached, SeedsEnabled: s.SeedsEnabled, SeedsFired: s.SeedsFired, Fired: s.Fired})
	}
	return out
}

func properties(c engine.Coverage) []propertyView {
	out := []propertyView{}
	for _, msg := range c.Messages() {
		p := c[msg]
		out = append(out, propertyView{Kind: p.Kind.String(), Message: msg, Location: p.Location, Problem: p.Problem(), SeedsReached: p.SeedsReached, SeedsTrue: p.SeedsTrue, Margin: p.Margin})
	}
	return out
}

func tail(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
