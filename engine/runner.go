package engine

import (
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
	Faults     []Fault
	Trace      []string
	Elapsed    time.Duration
	History    Linearizability
	Properties map[string]*Property
	Failure    Failure
}

type Runner struct {
	Doublecheck bool
	Mutation    Mutation
	weakened    atomic.Int64
	catalog     []CatalogEntry
	cfg         *Config
	rt          wazero.Runtime
	compiled    map[string]wazero.CompiledModule
}

func NewRunner(cfg *Config) (*Runner, error) {
	cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(cfg.Dir, ".seedfall", "cache"))
	if err != nil {
		return nil, err
	}
	entries, err := catalog(cfg)
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
	r := &Runner{cfg: cfg, rt: rt, compiled: map[string]wazero.CompiledModule{}, catalog: entries}
	return r, r.compile(ctx)
}

func (r *Runner) compile(ctx context.Context) error {
	byPath := map[string]wazero.CompiledModule{}
	for _, s := range r.cfg.all() {
		path := resolve(r.cfg.Dir, s.Wasm)
		if _, ok := byPath[path]; !ok {
			b, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("node %s: %w", s.Name, err)
			}
			if byPath[path], err = r.rt.CompileModule(ctx, instrument(b)); err != nil {
				return fmt.Errorf("compile %s: %w", s.Name, err)
			}
		}
		r.compiled[s.Name] = byPath[path]
	}
	return nil
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

func (r *Runner) schedule(seed uint64, faults []Fault) ([]Fault, bool) {
	if faults != nil {
		return faults, true
	}
	return r.Faults(seed), false
}

func (r *Runner) Run(seed uint64, faults []Fault) Result {
	res := r.once(seed, faults)
	if !r.Doublecheck {
		return res
	}
	if diff, diverged := divergence(res.Trace, r.once(seed, faults).Trace); diverged {
		res.Failed, res.At, res.Failure = true, 0, Failure{Class: FailNondeterminism}
		res.Reason = fmt.Sprintf("nondeterminism: two runs of seed %d diverge at %s", seed, diff)
	}
	return res
}

func (r *Runner) once(seed uint64, faults []Fault) Result {
	faults, explicit := r.schedule(seed, faults)
	started := time.Now()
	res := r.run(seed, faults, explicit, nil, nil)
	res.Elapsed = time.Since(started)
	return res
}

func divergence(a, b []string) (string, bool) {
	line := min(len(a), len(b))
	for i := range line {
		if a[i] != b[i] {
			line = i
			break
		}
	}
	if line == len(a) && line == len(b) {
		return "", false
	}
	return fmt.Sprintf("trace line %d: %q vs %q", line+1, lineAt(a, line), lineAt(b, line)), true
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<end of trace>"
}

func (r *Runner) run(seed uint64, faults []Fault, explicit bool, b *branch, p *probe) Result {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &world{
		cfg: r.cfg, seed: seed, ctx: ctx, cancel: cancel, rt: r.rt,
		sched: streams(seed, "sched"), netRand: streams(seed, "net"), diskRand: streams(seed, "disk"),
		events: make(chan event), faults: faults, trace: &tracer{}, compiled: r.compiled,
		explicit: explicit, ioRand: streams(seed, "io"), props: properties{}, spec: r.cfg.Faults.forSeed(seed), branch: b, probe: p,
		mutation: r.Mutation, weakened: func() { r.weakened.Add(1) },
	}
	w.net = newNetwork(w)
	res := Result{Seed: seed, Faults: faults}
	w.setup()
	w.loop()
	w.capture()
	w.shutdown()
	w.checkLinearizable()
	res.History = w.lin
	res.Properties = w.props
	res.Failed = w.failure != ""
	res.Reason = w.failure
	res.Failure = w.failureKind
	res.At = time.Duration(w.failAt)
	res.Duration = time.Duration(w.now)
	res.Finished = w.finished
	res.Steps = w.steps
	res.Trace = w.trace.lines
	res.Faults = mergeFaults(w.faults, w.ioFaults)
	return res
}

func (w *world) setup() {
	for _, s := range w.cfg.all() {
		n := &node{spec: s, crashable: s.Name != w.cfg.Workload.Name, compiled: w.compiled[s.Name], restartAt: -1}
		n.fs = newMemFS(func() int64 { return epochNanos + w.now })
		if w.spec.StrictDirs {
			n.fs.strict()
		}
		if n.crashable {
			n.fs.mutated = func() { w.onDiskOp(n) }
			n.fs.syncFails = func() bool { return w.fsyncFails(n) }
			n.fs.weaken, n.fs.onWeakened = w.mutation, w.weakened
		}
		n.skew = w.clockSkew(n)
		n.fs.special = func(p string) (fsapi.File, fsapi.Errno, bool) { return w.openSpecial(n, p) }
		w.nodes = append(w.nodes, n)
		if !n.crashable {
			w.workload = n
		}
	}
	for _, n := range w.nodes {
		w.start(n)
	}
}

type tracer struct {
	lines []string
}

func (t *tracer) add(at int64, format string, args ...any) {
	t.lines = append(t.lines, fmt.Sprintf("%10s  %s", fmtTime(at), strings.TrimSpace(fmt.Sprintf(format, args...))))
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
