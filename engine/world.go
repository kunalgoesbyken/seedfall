package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/sysfs"
	"github.com/tetratelabs/wazero/sys"
)

var (
	errKilled = errors.New("seedfall: instance killed")
	errFailed = errors.New("seedfall: run failed")
)

const epochNanos = int64(1767225600) * int64(time.Second)

type instKey struct{}

type node struct {
	spec      NodeSpec
	crashable bool
	compiled  wazero.CompiledModule
	fs        *memFS
	live      *instance
	starts    int
	ops       int64
	restartAt int64
}

func (n *node) name() string { return n.spec.Name }

type instance struct {
	node       *node
	label      string
	wakeAt     int64
	killed     bool
	crashUntil int64
	fds        map[int32]int32
	resume     chan bool
}

type event struct {
	inst *instance
	exit bool
	err  error
}

type partition struct {
	side  map[string]bool
	until int64
}

type world struct {
	cfg       *Config
	seed      uint64
	ctx       context.Context
	rt        wazero.Runtime
	now       int64
	sched     *rand.Rand
	netRand   *rand.Rand
	diskRand  *rand.Rand
	nodes     []*node
	workload  *node
	net       *network
	events    chan event
	faults    []Fault
	nextFault int
	part      *partition
	trace     *tracer
	failure   string
	failAt    int64
	finished  bool
	steps     int
	explicit  bool
	ioRand    *rand.Rand
	ioCrashes []Fault
	wasm      map[string][]byte
	history   history
	lin       Linearizability
	props     properties
}

func streams(seed uint64, name string) *rand.Rand {
	h := uint64(14695981039346656037)
	for _, b := range []byte(name) {
		h = (h ^ uint64(b)) * 1099511628211
	}
	return rand.New(rand.NewPCG(seed, h))
}

func (w *world) latency() int64 { return int64(uniform(w.netRand, w.cfg.Latency)) }

func (w *world) cutUntil(a, b string) (int64, bool) {
	if w.part == nil || w.now >= w.part.until || w.part.side[a] == w.part.side[b] {
		return 0, false
	}
	return w.part.until, true
}

func (w *world) cut(a, b string) bool {
	_, c := w.cutUntil(a, b)
	return c
}

func (w *world) instance(ctx context.Context) *instance {
	inst, _ := ctx.Value(instKey{}).(*instance)
	return inst
}

func readBytes(m api.Module, ptr, n uint32) []byte {
	b, ok := m.Memory().Read(ptr, n)
	if !ok {
		return nil
	}
	return append([]byte(nil), b...)
}

func (w *world) hostModule(ctx context.Context) error {
	b := w.rt.NewHostModuleBuilder("seedfall")
	export := func(name string, fn any) { b.NewFunctionBuilder().WithFunc(fn).Export(name) }
	export("net_listen", func(ctx context.Context, m api.Module, p, n uint32) int32 {
		return w.net.listen(w.instance(ctx), string(readBytes(m, p, n)))
	})
	export("net_dial", func(ctx context.Context, m api.Module, p, n uint32) int32 {
		return w.net.dial(w.instance(ctx), string(readBytes(m, p, n)))
	})
	export("net_accept", func(ctx context.Context, id int32) int32 {
		return w.net.accept(w.instance(ctx), id)
	})
	export("net_bind", func(ctx context.Context, fd, id int32) {
		w.instance(ctx).fds[fd] = id
	})
	export("history_call", func(ctx context.Context, m api.Module, p, n uint32) int32 {
		return w.history.call(w.now, readBytes(m, p, n))
	})
	export("history_return", func(ctx context.Context, m api.Module, id int32, p, n uint32, outcome int32) {
		w.history.complete(w.now, id, string(readBytes(m, p, n)), outcome)
	})
	export("property", func(ctx context.Context, m api.Module, kind, cond int32, p, n, dp, dn uint32) {
		w.observeProperty(w.instance(ctx), PropertyKind(kind), cond != 0, string(readBytes(m, p, n)), string(readBytes(m, dp, dn)))
	})
	export("fail", func(ctx context.Context, m api.Module, p, n uint32) {
		inst := w.instance(ctx)
		w.fail(fmt.Sprintf("%s: %s", inst.label, readBytes(m, p, n)))
		panic(errFailed)
	})
	_, err := b.Instantiate(ctx)
	return err
}

func (w *world) fail(reason string) {
	if w.failure == "" {
		w.failure, w.failAt = reason, w.now
		w.trace.add(w.now, "FAIL  %s", reason)
	}
}

func (w *world) moduleConfig(inst *instance) wazero.ModuleConfig {
	n := inst.node
	key := [32]byte{}
	r := streams(w.seed, fmt.Sprintf("%s/%d", n.name(), n.starts))
	for i := range key {
		key[i] = byte(r.Uint32())
	}
	out := &lineWriter{emit: func(line string) { w.trace.add(w.now, "%-8s| %s", inst.label, line) }}
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(append([]string{n.name()}, n.spec.Args...)...).
		WithEnv("SEEDFALL", "1").
		WithEnv("SEEDFALL_NODE", n.name()).
		WithStdout(out).WithStderr(out).
		WithRandSource(rand.NewChaCha8(key)).
		WithWalltime(func() (int64, int32) {
			t := epochNanos + w.now
			return t / int64(time.Second), int32(t % int64(time.Second))
		}, 1).
		WithNanotime(func() int64 { return epochNanos + w.now }, 1).
		WithNanosleep(func(ns int64) { w.park(inst, w.now+max(ns, 0)) }).
		WithOsyield(func() {}).
		WithFSConfig(wazero.NewFSConfig().(sysfs.FSConfig).WithSysFSMount(n.fs, "/"))
	for _, k := range sortedKeys(n.spec.Env) {
		cfg = cfg.WithEnv(k, n.spec.Env[k])
	}
	return cfg
}

func (w *world) park(inst *instance, at int64) {
	inst.wakeAt = at
	w.events <- event{inst: inst}
	if !<-inst.resume {
		panic(errKilled)
	}
}

func (w *world) start(n *node) {
	n.starts++
	inst := &instance{node: n, label: fmt.Sprintf("%s#%d", n.name(), n.starts), wakeAt: w.now, resume: make(chan bool), fds: map[int32]int32{}}
	n.live, n.restartAt = inst, -1
	ctx := context.WithValue(w.ctx, instKey{}, inst)
	cfg := w.moduleConfig(inst)
	go func() {
		if !<-inst.resume {
			w.events <- event{inst: inst, exit: true, err: errKilled}
			return
		}
		mod, err := w.rt.InstantiateModule(ctx, n.compiled, cfg)
		if mod != nil {
			mod.Close(context.Background())
		}
		w.events <- event{inst: inst, exit: true, err: err}
	}()
	w.trace.add(w.now, "start %s", inst.label)
}

func exitedCleanly(err error) bool {
	var ee *sys.ExitError
	return err == nil || (errors.As(err, &ee) && ee.ExitCode() == 0)
}

func (w *world) resume(inst *instance) {
	w.now = max(w.now, inst.wakeAt)
	w.steps++
	inst.resume <- true
	w.handle(<-w.events)
}

func (w *world) handle(ev event) {
	if !ev.exit {
		return
	}
	n := ev.inst.node
	if n.live == ev.inst {
		n.live = nil
	}
	w.net.drop(ev.inst)
	switch {
	case w.failure != "":
	case ev.inst.killed:
		if ev.inst.crashUntil > 0 {
			w.crashDisk(n, ev.inst.crashUntil)
		}
	case w.ctx.Err() != nil:
		w.fail(fmt.Sprintf("watchdog: %s used more than %s of real time without finishing (livelock or busy loop)", ev.inst.label, time.Duration(w.cfg.Watchdog)))
	case n == w.workload && exitedCleanly(ev.err):
		w.finished = true
		w.trace.add(w.now, "workload finished")
	case exitedCleanly(ev.err):
		w.fail(fmt.Sprintf("%s exited", ev.inst.label))
	default:
		w.fail(fmt.Sprintf("%s crashed: %v", ev.inst.label, ev.err))
	}
}

func (w *world) kill(inst *instance) {
	inst.killed = true
	inst.resume <- false
	w.handle(<-w.events)
}

func (w *world) applyFault(f Fault) {
	switch f.Kind {
	case FaultCrash:
		n := w.nodeByName(f.Node)
		if n == nil || n.live == nil || !n.crashable {
			return
		}
		w.trace.add(w.now, "fault crash %s, restart at %s", n.live.label, fmtTime(int64(f.Until)))
		w.kill(n.live)
		w.crashDisk(n, int64(f.Until))
	case FaultPartition:
		side := map[string]bool{}
		for _, s := range f.Side {
			side[s] = true
		}
		w.part = &partition{side: side, until: int64(f.Until)}
		w.trace.add(w.now, "fault partition %v | rest, heals at %s", f.Side, fmtTime(int64(f.Until)))
	}
}

func (w *world) crashDisk(n *node, until int64) {
	n.fs.crash(w.diskRand, w.cfg.Faults.TornWrites)
	n.restartAt = until
}

func (w *world) onDiskOp(n *node) {
	inst := n.live
	n.ops++
	until, crash := w.ioCrashDue(n)
	if !crash {
		return
	}
	w.trace.add(w.now, "fault crash %s during disk op %d, restart at %s", inst.label, n.ops, fmtTime(until))
	inst.killed, inst.crashUntil = true, until
	panic(errKilled)
}

func (w *world) ioCrashDue(n *node) (int64, bool) {
	delay, crash := w.ioCrashDelay(n)
	if !crash {
		return 0, false
	}
	until := w.now + delay
	w.ioCrashes = append(w.ioCrashes, Fault{At: Duration(w.now), Kind: FaultCrashIO, Node: n.name(), Op: n.ops, Until: Duration(until)})
	return until, true
}

func (w *world) ioCrashDelay(n *node) (int64, bool) {
	if w.explicit {
		for _, f := range w.faults {
			if f.Kind == FaultCrashIO && f.Node == n.name() && f.Op == n.ops {
				return int64(f.Until - f.At), true
			}
		}
		return 0, false
	}
	if w.cfg.Faults.CrashOnIO <= 0 || w.ioRand.Float64() >= w.cfg.Faults.CrashOnIO {
		return 0, false
	}
	return int64(uniform(w.ioRand, w.cfg.Faults.RestartAfter)), true
}

func (w *world) checkLinearizable() {
	if w.failure != "" {
		return
	}
	w.lin = checkHistory(&w.history, w.now)
	if w.lin.Checked {
		w.trace.add(w.now, "checked %d operations for linearizability: %s", w.lin.Ops, map[bool]string{true: "ok", false: "VIOLATION"}[w.lin.Ok])
	}
	if w.lin.Checked && !w.lin.Ok {
		w.fail(fmt.Sprintf("history of %d operations is not linearizable", w.lin.Ops))
	}
}

func (w *world) nodeByName(name string) *node {
	for _, n := range w.nodes {
		if n.name() == name {
			return n
		}
	}
	return nil
}

type step struct {
	at   int64
	kind int
	node *node
}

const (
	stepFault = iota
	stepRestart
	stepResume
)

func (w *world) nextSteps() []step {
	var steps []step
	if w.nextFault < len(w.faults) {
		steps = append(steps, step{at: int64(w.faults[w.nextFault].At), kind: stepFault})
	}
	for _, n := range w.nodes {
		switch {
		case n.live != nil:
			steps = append(steps, step{at: n.live.wakeAt, kind: stepResume, node: n})
		case n.restartAt >= 0:
			steps = append(steps, step{at: n.restartAt, kind: stepRestart, node: n})
		}
	}
	if len(steps) == 0 {
		return nil
	}
	earliest := slices.MinFunc(steps, func(a, b step) int { return compareStep(a, b) })
	return slices.DeleteFunc(steps, func(s step) bool { return s.at != earliest.at || s.kind != earliest.kind })
}

func compareStep(a, b step) int {
	if a.at != b.at {
		return int(a.at - b.at)
	}
	return a.kind - b.kind
}

func (w *world) loop() {
	horizon := int64(w.cfg.Duration)
	for w.failure == "" && !w.finished {
		candidates := w.nextSteps()
		if len(candidates) == 0 {
			w.fail("deadlock: every node has exited")
			return
		}
		if candidates[0].at == never {
			w.fail("deadlock: every node is blocked with no timer or pending message")
			return
		}
		s := candidates[w.sched.IntN(len(candidates))]
		if s.at > horizon {
			w.trace.add(w.now, "horizon %s reached before the workload finished", fmtTime(horizon))
			return
		}
		switch s.kind {
		case stepFault:
			w.now = max(w.now, s.at)
			w.applyFault(w.faults[w.nextFault])
			w.nextFault++
		case stepRestart:
			w.now = max(w.now, s.at)
			w.start(s.node)
		case stepResume:
			w.resume(s.node.live)
		}
	}
}

func (w *world) shutdown() {
	for _, n := range w.nodes {
		if n.live != nil {
			n.live.killed = true
			n.live.resume <- false
			<-w.events
			n.live = nil
		}
	}
}

func fmtTime(ns int64) string {
	return fmt.Sprintf("%.3fs", time.Duration(ns).Seconds())
}

type lineWriter struct {
	buf  []byte
	emit func(string)
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := slices.Index(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.emit(strings.TrimRight(string(l.buf[:i]), "\r"))
		l.buf = l.buf[i+1:]
	}
}
