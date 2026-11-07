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
	Seed     uint64
	Failed   bool
	Reason   string
	At       time.Duration
	Finished bool
	Steps    int
	Faults   []Fault
	Trace    []string
	Elapsed  time.Duration
	History  Linearizability
}

type Runner struct {
	cfg   *Config
	cache wazero.CompilationCache
	wasm  map[string][]byte
}

func NewRunner(cfg *Config) (*Runner, error) {
	cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(cfg.Dir, ".seedfall", "cache"))
	if err != nil {
		return nil, err
	}
	r := &Runner{cfg: cfg, cache: cache, wasm: map[string][]byte{}}
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
	return generateFaults(r.cfg, streams(seed, "faults"))
}

func (r *Runner) Run(seed uint64, faults []Fault) Result {
	explicit := faults != nil
	if !explicit {
		faults = r.Faults(seed)
	}
	started := time.Now()
	res := r.run(seed, faults, explicit)
	res.Elapsed = time.Since(started)
	return res
}

func (r *Runner) run(seed uint64, faults []Fault, explicit bool) Result {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(r.cfg.Watchdog))
	defer cancel()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(r.cache).WithCloseOnContextDone(true))
	defer rt.Close(context.Background())
	w := &world{
		cfg: r.cfg, seed: seed, ctx: ctx, rt: rt,
		sched: streams(seed, "sched"), netRand: streams(seed, "net"), diskRand: streams(seed, "disk"),
		events: make(chan event), faults: faults, trace: &tracer{}, wasm: r.wasm,
		explicit: explicit, ioRand: streams(seed, "io"),
	}
	w.net = newNetwork(w)
	res := Result{Seed: seed, Faults: faults}
	if err := w.setup(ctx); err != nil {
		res.Failed, res.Reason = true, err.Error()
		return res
	}
	w.loop()
	w.shutdown()
	w.checkLinearizable()
	res.History = w.lin
	res.Failed = w.failure != ""
	res.Reason = w.failure
	res.At = time.Duration(w.failAt)
	res.Finished = w.finished
	res.Steps = w.steps
	res.Trace = w.trace.lines
	res.Faults = mergeFaults(faults, w.ioCrashes)
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
		if w.cfg.Faults.StrictDirs {
			n.fs.strict()
		}
		if n.crashable {
			n.fs.mutated = func() { w.onDiskOp(n) }
		}
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
	out := slices.DeleteFunc(slices.Clone(planned), func(f Fault) bool { return f.Kind == FaultCrashIO })
	out = append(out, observed...)
	slices.SortStableFunc(out, func(a, b Fault) int { return int(a.At - b.At) })
	return out
}
