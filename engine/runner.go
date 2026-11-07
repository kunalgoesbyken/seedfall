package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	fsapi "github.com/tetratelabs/wazero/experimental/sys"
)

type Result struct {
	Seed       uint64
	Failed     bool
	Reason     string
	At         time.Duration
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
	catalog     []CatalogEntry
	cfg         *Config
	cache       wazero.CompilationCache
	wasm        map[string][]byte
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
	r := &Runner{cfg: cfg, cache: cache, wasm: map[string][]byte{}, catalog: entries}
	for _, s := range cfg.all() {
		b, err := os.ReadFile(resolve(cfg.Dir, s.Wasm))
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", s.Name, err)
		}
		r.wasm[s.Name] = b
	}
	return r, r.warm()
}

func (r *Runner) warm() error {
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(r.cache))
	defer rt.Close(ctx)
	for name, b := range r.wasm {
		if _, err := rt.CompileModule(ctx, b); err != nil {
			return fmt.Errorf("compile %s: %w", name, err)
		}
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

func (r *Runner) Run(seed uint64, faults []Fault) Result {
	explicit := faults != nil
	if !explicit {
		faults = r.Faults(seed)
	}
	res := r.timedRun(seed, faults, explicit)
	if !r.Doublecheck {
		return res
	}
	again := r.timedRun(seed, faults, explicit)
	if line, diverged := divergence(res.Trace, again.Trace); diverged {
		res.Failed, res.At, res.Failure = true, 0, Failure{Class: FailNondeterminism}
		res.Reason = fmt.Sprintf("nondeterminism: two runs of seed %d diverge at trace line %d: %q vs %q", seed, line+1, lineAt(res.Trace, line), lineAt(again.Trace, line))
	}
	return res
}

func (r *Runner) timedRun(seed uint64, faults []Fault, explicit bool) Result {
	started := time.Now()
	res := r.run(seed, faults, explicit, nil)
	res.Elapsed = time.Since(started)
	return res
}

func divergence(a, b []string) (int, bool) {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i, true
		}
	}
	return min(len(a), len(b)), len(a) != len(b)
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<end of trace>"
}

func (r *Runner) run(seed uint64, faults []Fault, explicit bool, b *branch) Result {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(r.cache).WithCloseOnContextDone(true))
	defer rt.Close(context.Background())
	w := &world{
		cfg: r.cfg, seed: seed, ctx: ctx, cancel: cancel, rt: rt,
		sched: streams(seed, "sched"), netRand: streams(seed, "net"), diskRand: streams(seed, "disk"),
		events: make(chan event), faults: faults, trace: &tracer{}, wasm: r.wasm,
		explicit: explicit, ioRand: streams(seed, "io"), props: properties{}, spec: r.cfg.Faults.forSeed(seed), branch: b,
	}
	w.net = newNetwork(w)
	res := Result{Seed: seed, Faults: faults}
	if err := w.setup(ctx); err != nil {
		res.Failed, res.Reason, res.Failure = true, err.Error(), Failure{Class: FailSetup}
		return res
	}
	w.loop()
	w.shutdown()
	w.checkLinearizable()
	res.History = w.lin
	res.Properties = w.props
	res.Failed = w.failure != ""
	res.Reason = w.failure
	res.Failure = w.failureKind
	res.At = time.Duration(w.failAt)
	res.Finished = w.finished
	res.Steps = w.steps
	res.Trace = w.trace.lines
	res.Faults = mergeFaults(faults, w.ioFaults)
	return res
}

func (w *world) setup(ctx context.Context) error {
	if err := w.wasiModule(ctx); err != nil {
		return err
	}
	if err := w.hostModule(ctx); err != nil {
		return err
	}
	for _, s := range w.cfg.all() {
		compiled, err := w.rt.CompileModule(ctx, w.wasm[s.Name])
		if err != nil {
			return fmt.Errorf("compile %s: %w", s.Name, err)
		}
		n := &node{spec: s, crashable: s.Name != w.cfg.Workload.Name, compiled: compiled, restartAt: -1}
		n.fs = newMemFS(func() int64 { return epochNanos + w.now })
		if w.spec.StrictDirs {
			n.fs.strict()
		}
		if n.crashable {
			n.fs.mutated = func() { w.onDiskOp(n) }
			n.fs.syncFails = func() bool { return w.fsyncFails(n) }
		}
		n.skew = w.clockSkew(n)
		n.fs.special = func(p string) (fsapi.File, fsapi.Errno, bool) { return w.openNetFile(n, p) }
		w.nodes = append(w.nodes, n)
		if !n.crashable {
			w.workload = n
		}
	}
	for _, n := range w.nodes {
		w.start(n)
	}
	return nil
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

func (r Result) FaultsBefore() []Fault {
	return slices.DeleteFunc(slices.Clone(r.Faults), func(f Fault) bool { return time.Duration(f.At) > r.At })
}
