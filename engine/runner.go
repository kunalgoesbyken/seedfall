package engine

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	fsapi "github.com/tetratelabs/wazero/experimental/sys"
)

type Result struct {
	Seed       uint64
	Failed     bool
	Reason     string
	At         time.Duration
	Duration   time.Duration
	Finished   bool
	Steps      int
	Horizon    time.Duration
	Ops        int64
	Faults     []Fault
	Trace      []string
	Elapsed    time.Duration
	History    HistoryCheck
	Properties map[string]*Property
	Events     []Event
	Failure    Failure
	Buggify    map[string]*BuggifySite
	HostCalls  int64
	Timeline   *Timeline

	hostHash    uint64
	checkpoints []uint64
	captured    []hostCall
}

type Runner struct {
	Doublecheck bool
	NetTrace    bool
	Mutation    Mutation
	weakened    atomic.Int64
	catalog     []CatalogEntry
	cfg         *Config
	rt          wazero.Runtime
	compiled    map[string]wazero.CompiledModule
	upgrades    map[string]wazero.CompiledModule
}

func NewRunner(cfg *Config) (*Runner, error) {
	entries, err := catalog(cfg)
	if err != nil {
		return nil, err
	}
	paths, upgrades := map[string]string{}, map[string]string{}
	var order []string
	modules := map[string][]byte{}
	add := func(name, wasm string) (string, error) {
		path := resolve(cfg.Dir, wasm)
		if _, ok := modules[path]; ok {
			return path, nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", name, err)
		}
		if modules[path], err = instrument(b, cfg.Preempt.Loops); err != nil {
			return "", fmt.Errorf("node %s: %w", name, err)
		}
		order = append(order, path)
		return path, nil
	}
	for _, s := range cfg.all() {
		if paths[s.Name], err = add(s.Name, s.Wasm); err != nil {
			return nil, err
		}
		if s.upgradeWasm == "" {
			continue
		}
		if upgrades[s.Name], err = add(s.Name, s.upgradeWasm); err != nil {
			return nil, err
		}
	}
	bytes := make([][]byte, len(order))
	for i, p := range order {
		bytes[i] = modules[p]
	}
	cache, err := compilationCache(filepath.Join(cfg.Dir, ".seedfall", "cache"), bytes)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(cache).WithCloseOnContextDone(true))
	if err := wasiModule(ctx, rt); err != nil {
		return nil, err
	}
	if err := hostModule(ctx, rt); err != nil {
		return nil, err
	}
	r := &Runner{cfg: cfg, rt: rt, compiled: map[string]wazero.CompiledModule{}, upgrades: map[string]wazero.CompiledModule{}, catalog: entries}
	byPath := map[string]wazero.CompiledModule{}
	for _, p := range order {
		if byPath[p], err = rt.CompileModule(ctx, modules[p]); err != nil {
			return nil, fmt.Errorf("compile %s: %w", filepath.Base(p), err)
		}
	}
	for name, p := range paths {
		r.compiled[name] = byPath[p]
	}
	for name, p := range upgrades {
		r.upgrades[name] = byPath[p]
	}
	return r, nil
}

func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func (r *Runner) Faults(seed uint64) []Fault {
	return generateFaults(r.cfg, r.cfg.Faults.forSeed(seed), streams(seed, "faults"))
}

type Schedule struct {
	Faults   []Fault  `json:"faults"`
	Duration Duration `json:"duration,omitempty"`
	Ops      int64    `json:"ops,omitempty"`
}

func (r *Runner) schedule(seed uint64, s Schedule) (Schedule, bool) {
	if s.Faults != nil {
		return s, true
	}
	s.Faults = r.Faults(seed)
	return s, false
}

func (r *Runner) Run(seed uint64, faults []Fault) Result {
	return r.Replay(seed, Schedule{Faults: faults})
}

func (r *Runner) Replay(seed uint64, s Schedule) Result {
	return r.doublecheck(seed, s, r.once(seed, s, 0))
}

func (r *Runner) doublecheck(seed uint64, s Schedule, res Result) Result {
	if !r.Doublecheck {
		return res
	}
	if diff, diverged := r.divergence(seed, s, res, r.once(seed, s, 0)); diverged {
		res.Failed, res.At, res.Failure = true, 0, Failure{Class: FailNondeterminism}
		res.Reason = fmt.Sprintf("nondeterminism: two runs of seed %d diverge at %s", seed, diff)
	}
	return res
}

func (r *Runner) divergence(seed uint64, s Schedule, a, b Result) (string, bool) {
	window, diverged := firstHostWindow(a, b)
	if !diverged {
		return divergence(a.Trace, b.Trace)
	}
	diff := hostDivergence(window, r.once(seed, s, window), r.once(seed, s, window))
	if line, ok := divergence(a.Trace, b.Trace); ok {
		diff += "; " + line
	}
	return diff, true
}

func (r *Runner) once(seed uint64, s Schedule, capture int64) Result {
	return r.timed(seed, s, runOptions{capture: capture})
}

func (r *Runner) Record(seed uint64, s Schedule) Result {
	return r.timed(seed, s, runOptions{record: true})
}

func (r *Runner) timed(seed uint64, s Schedule, o runOptions) Result {
	s, o.explicit = r.schedule(seed, s)
	started := time.Now()
	res := r.run(seed, s, o)
	res.Elapsed = time.Since(started)
	return res
}

func divergence(a, b []string) (string, bool) {
	line, diverged := FirstDivergence(a, b)
	if !diverged {
		return "", false
	}
	return fmt.Sprintf("trace line %d: %q vs %q", line+1, LineAt(a, line), LineAt(b, line)), true
}

func FirstDivergence(a, b []string) (int, bool) {
	line := min(len(a), len(b))
	for i := range line {
		if a[i] != b[i] {
			line = i
			break
		}
	}
	return line, line != len(a) || line != len(b)
}

func LineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<end of trace>"
}

type runOptions struct {
	explicit bool
	branch   *branch
	probe    *probe
	capture  int64
	record   bool
	debug    *debugger
}

func (r *Runner) run(seed uint64, s Schedule, o runOptions) Result {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &world{
		cfg: r.cfg, seed: seed, ctx: ctx, cancel: cancel, rt: r.rt,
		sched: streams(seed, "sched"), netRand: streams(seed, "net"), diskRand: streams(seed, "disk"), latRand: streams(seed, "disklatency"), mutantRand: streams(seed, "mutant"),
		events: make(chan event), faults: slices.Clone(s.Faults), horizon: int64(cmp.Or(s.Duration, r.cfg.Duration)), opsLimit: s.Ops, trace: &tracer{detail: o.record || o.debug != nil}, compiled: r.compiled,
		upgrades: r.upgrades, explicit: o.explicit, ioRand: streams(seed, "io"), props: properties{}, spec: r.cfg.Faults.forSeed(seed), branch: o.branch, probe: o.probe,
		mutation: r.Mutation, weakened: func() { r.weakened.Add(1) }, shielded: map[string]bool{}, deadline: never,
		history: history{model: r.cfg.Checker.Model}, calls: newHostCalls(o.capture), netTrace: r.NetTrace, debug: o.debug,
	}
	if o.record {
		w.rec = &recorder{}
	}
	w.net = newNetwork(w)
	res := Result{Seed: seed, Faults: s.Faults, Horizon: time.Duration(w.horizon)}
	w.setup()
	w.loop()
	w.preemptSummary()
	w.debugEnd()
	w.capture()
	w.shutdown()
	w.checkHistory()
	res.History = w.lin
	res.Properties = w.props
	res.Events = w.sent
	res.HostCalls, res.hostHash, res.checkpoints, res.captured = w.calls.n, w.calls.hash, w.calls.checkpoints, w.calls.captured
	res.Buggify = w.bugs.sites
	res.Failed = w.failure != ""
	res.Reason = w.failure
	res.Failure = w.failureKind
	res.At = time.Duration(w.failAt)
	res.Duration = time.Duration(w.now)
	res.Finished = w.finished
	res.Steps = w.steps
	res.Ops = w.opsWanted
	res.Trace = w.trace.lines
	res.Faults = mergeFaults(w.faults, w.ioFaults)
	if w.rec != nil {
		res.Timeline = w.timeline(res)
	}
	return res
}

func (w *world) setup() {
	if mode := w.cfg.Preempt.describe(); mode != "" {
		w.trace.add(w.now, "%s", mode)
	}
	for _, s := range w.cfg.all() {
		n := &node{spec: s, crashable: w.cfg.faultable(s), workload: w.cfg.isWorkload(s.Name), compiled: w.compiled[s.Name], upgrade: w.upgrades[s.Name], restartAt: -1}
		n.fs = newMemFS(func() int64 { return epochNanos + w.now })
		if w.spec.StrictDirs {
			n.fs.strict()
		}
		if files, size := n.fs.seed(w.cfg.disks[s.Name]); files > 0 {
			w.trace.add(w.now, "%s's disk starts with %d durable files, %d bytes", s.Name, files, size)
		}
		if n.crashable {
			n.fs.mutated = func() { w.onDiskOp(n) }
			n.fs.syncFails = func() bool { return w.fsyncFails(n) }
			n.fs.weaken, n.fs.onWeakened = w.mutation, w.weakened
			w.wireDisk(n)
		}
		n.skew = w.clockSkew(n)
		w.planChanges(n)
		n.fs.special = func(p string) (fsapi.File, fsapi.Errno, bool) { return w.openSpecial(n, p) }
		n.fs.observe = func(op, p string, arg int64) { w.hostCall(n.live, op, p, arg) }
		w.nodes = append(w.nodes, n)
		w.shielded[s.Name] = !w.cfg.networked(s)
		if w.cfg.isFinally(s.Name) {
			w.finally = n
		}
	}
	for _, n := range w.nodes {
		if n != w.finally {
			w.start(n)
		}
	}
}

type TraceLine struct {
	At       time.Duration `json:"at"`
	Node     string        `json:"node,omitempty"`
	Instance string        `json:"instance,omitempty"`
	Output   string        `json:"output,omitempty"`
	Text     string        `json:"text"`
}

type tracer struct {
	lines   []string
	entries []TraceLine
	detail  bool
}

func (t *tracer) add(at int64, format string, args ...any) {
	t.about(at, TraceLine{}, format, args...)
}

func (t *tracer) about(at int64, who TraceLine, format string, args ...any) {
	text := fmt.Sprintf("%10s  %s", fmtTime(at), strings.TrimSpace(fmt.Sprintf(format, args...)))
	t.lines = append(t.lines, text)
	if t.detail {
		who.At, who.Text = time.Duration(at), text
		t.entries = append(t.entries, who)
	}
}

func nodeLine(name string) TraceLine { return TraceLine{Node: name} }

func instLine(inst *instance) TraceLine {
	return TraceLine{Node: inst.node.name(), Instance: inst.label}
}

func mergeFaults(planned, observed []Fault) []Fault {
	out := slices.DeleteFunc(slices.Clone(planned), func(f Fault) bool { return observedAtRuntime(f.Kind) })
	out = append(out, observed...)
	slices.SortStableFunc(out, func(a, b Fault) int { return int(a.At - b.At) })
	return out
}

func (r Result) FaultsBefore() []Fault { return r.faultsUntil(r.At) }

func (r Result) FaultsApplied() []Fault { return r.faultsUntil(r.Duration) }

func (r Result) faultsUntil(t time.Duration) []Fault {
	return slices.DeleteFunc(slices.Clone(r.Faults), func(f Fault) bool { return time.Duration(f.At) > t })
}
