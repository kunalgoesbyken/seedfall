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

type (
	instKey  struct{}
	worldKey struct{}
)

type node struct {
	spec       NodeSpec
	crashable  bool
	workload   bool
	done       bool
	compiled   wazero.CompiledModule
	fs         *memFS
	live       *instance
	starts     int
	ops        int64
	reads      int64
	restartAt  int64
	skew       int64
	pausedTo   int64
	slowUntil  int64
	slowFactor float64
	monoShift  int64
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
	preempt    *preemption
}

type event struct {
	inst *instance
	exit bool
	err  error
}

type world struct {
	cfg         *Config
	seed        uint64
	ctx         context.Context
	cancel      context.CancelFunc
	rt          wazero.Runtime
	now         int64
	sched       *rand.Rand
	netRand     *rand.Rand
	diskRand    *rand.Rand
	latRand     *rand.Rand
	mutantRand  *rand.Rand
	nodes       []*node
	finally     *node
	shielded    map[string]bool
	recovering  bool
	deadline    int64
	net         *network
	events      chan event
	faults      []Fault
	horizon     int64
	opsLimit    int64
	opsWanted   int64
	nextFault   int
	netFaults   []Fault
	trace       *tracer
	failure     string
	failAt      int64
	failureKind Failure
	finished    bool
	steps       int
	explicit    bool
	ioRand      *rand.Rand
	ioFaults    []Fault
	scheduled   map[string]bool
	spec        FaultSpec
	compiled    map[string]wazero.CompiledModule
	history     history
	lin         HistoryCheck
	props       properties
	sent        []Event
	calls       hostCalls
	branch      *branch
	probe       *probe
	mutation    Mutation
	weakened    func()
	setupDone   bool
	quietUntil  int64
	bugs        buggifyState
}

func streams(seed uint64, name string) *rand.Rand {
	h := uint64(14695981039346656037)
	for _, b := range []byte(name) {
		h = (h ^ uint64(b)) * 1099511628211
	}
	return rand.New(rand.NewPCG(seed, h))
}

func worldOf(ctx context.Context) *world {
	w, _ := ctx.Value(worldKey{}).(*world)
	return w
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

func hostModule(ctx context.Context, rt wazero.Runtime) error {
	b := rt.NewHostModuleBuilder("seedfall")
	export := func(name string, fn any) { b.NewFunctionBuilder().WithFunc(fn).Export(name) }
	export("net_listen", func(ctx context.Context, m api.Module, p, n uint32) int32 {
		w := worldOf(ctx)
		addr := string(readBytes(m, p, n))
		id := w.net.listen(w.instance(ctx), addr)
		w.hostCall(w.instance(ctx), "net_listen", addr, int64(id))
		return id
	})
	export("net_dial", func(ctx context.Context, m api.Module, p, n uint32) int32 {
		w := worldOf(ctx)
		addr := string(readBytes(m, p, n))
		id := w.net.dial(w.instance(ctx), addr)
		w.hostCall(w.instance(ctx), "net_dial", addr, int64(id))
		return id
	})
	export("net_accept", func(ctx context.Context, id int32) int32 {
		w := worldOf(ctx)
		conn := w.net.accept(w.instance(ctx), id)
		w.hostCall(w.instance(ctx), "net_accept", "", int64(conn))
		return conn
	})
	export("net_bind", func(ctx context.Context, fd, id int32) {
		worldOf(ctx).instance(ctx).fds[fd] = id
	})
	export("history_call", func(ctx context.Context, m api.Module, p, n uint32) int32 {
		w := worldOf(ctx)
		inst := w.instance(ctx)
		id, err := w.history.call(w.now, readBytes(m, p, n), inst.label)
		w.hostCall(inst, "history_call", "", int64(id))
		w.historyError(inst, err)
		return id
	})
	export("history_return", func(ctx context.Context, m api.Module, id int32, p, n uint32, outcome int32) {
		w := worldOf(ctx)
		w.hostCall(w.instance(ctx), "history_return", "", int64(id))
		w.historyError(w.instance(ctx), w.history.complete(w.now, id, readBytes(m, p, n), outcome))
	})
	export("property", func(ctx context.Context, m api.Module, kind, cond int32, p, n, dp, dn uint32) {
		w := worldOf(ctx)
		msg := string(readBytes(m, p, n))
		w.hostCall(w.instance(ctx), "property", msg, int64(cond))
		w.observeProperty(w.instance(ctx), PropertyKind(kind), cond != 0, msg, string(readBytes(m, dp, dn)))
	})
	export("numeric", func(ctx context.Context, m api.Module, kind, cond int32, margin float64, p, n, dp, dn uint32) {
		w := worldOf(ctx)
		msg := string(readBytes(m, p, n))
		w.hostCall(w.instance(ctx), "numeric", msg, int64(cond))
		w.observeNumeric(w.instance(ctx), PropertyKind(kind), cond != 0, margin, msg, string(readBytes(m, dp, dn)))
	})
	export("event", func(ctx context.Context, m api.Module, p, n, dp, dn uint32) {
		w := worldOf(ctx)
		name := string(readBytes(m, p, n))
		w.hostCall(w.instance(ctx), "event", name, 0)
		w.event(w.instance(ctx), name, string(readBytes(m, dp, dn)))
	})
	export("buggify", func(ctx context.Context, m api.Module, p, n uint32, prob float64) int32 {
		w := worldOf(ctx)
		site := string(readBytes(m, p, n))
		fired := w.buggify(w.instance(ctx), site, prob)
		w.hostCall(w.instance(ctx), "buggify", site, int64(boolInt(fired)))
		return boolInt(fired)
	})
	export("setup_complete", func(ctx context.Context) {
		w := worldOf(ctx)
		w.hostCall(w.instance(ctx), "setup_complete", "", 0)
		w.setupComplete(w.instance(ctx))
	})
	export("quiet", func(ctx context.Context, ns int64) {
		w := worldOf(ctx)
		w.hostCall(w.instance(ctx), "quiet", "", ns)
		w.quietFor(w.instance(ctx), time.Duration(ns))
	})
	export("budget", func(ctx context.Context, n int64) int64 {
		w := worldOf(ctx)
		got := w.budget(w.instance(ctx), n)
		w.hostCall(w.instance(ctx), "budget", "", got)
		return got
	})
	export("fail", func(ctx context.Context, m api.Module, p, n uint32) {
		w := worldOf(ctx)
		inst := w.instance(ctx)
		w.fail(Failure{Class: FailAssert, Node: inst.node.name()}, fmt.Sprintf("%s: %s", inst.label, readBytes(m, p, n)))
		panic(errFailed)
	})
	_, err := b.Instantiate(ctx)
	return err
}

func (w *world) fail(kind Failure, reason string) {
	if w.failure == "" {
		w.failure, w.failAt, w.failureKind = reason, w.now, kind
		w.trace.add(w.now, "FAIL  %s", reason)
		w.capture()
	}
}

func (w *world) moduleConfig(inst *instance) wazero.ModuleConfig {
	n := inst.node
	out := &lineWriter{emit: func(line string) { w.trace.add(w.now, "%-8s| %s", inst.label, line) }}
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(append([]string{n.name()}, n.spec.Args...)...).
		WithEnv("SEEDFALL", "1").
		WithEnv("SEEDFALL_NODE", n.name()).
		WithEnv(antithesisOutputEnv, "/"+antithesisOutputPath).
		WithStdout(out).WithStderr(out).
		WithRandSource(w.guestRandom(inst, fmt.Sprintf("%s/%d", n.name(), n.starts))).
		WithStartFunctions().
		WithWalltime(func() (int64, int32) {
			t := epochNanos + w.tick() + n.skew
			w.hostCall(inst, "clock_walltime", "", t)
			return t / int64(time.Second), int32(t % int64(time.Second))
		}, 1).
		WithNanotime(func() int64 {
			t := epochNanos + w.tick() + n.monoShift
			w.hostCall(inst, "clock_nanotime", "", t)
			return t
		}, 1).
		WithNanosleep(func(ns int64) { w.park(inst, w.now+w.stretch(max(ns, 0), n)) }).
		WithFSConfig(wazero.NewFSConfig().(sysfs.FSConfig).WithSysFSMount(n.fs, "/"))
	for _, k := range sortedKeys(n.spec.Env) {
		cfg = cfg.WithEnv(k, n.spec.Env[k])
	}
	return cfg
}

func (w *world) park(inst *instance, at int64) {
	w.hostCall(inst, "park", "", at)
	inst.wakeAt = at
	w.events <- event{inst: inst}
	if !<-inst.resume {
		panic(errKilled)
	}
	inst.preempt.reset()
}

func (w *world) tick() int64 {
	w.now++
	return w.now
}

func (w *world) start(n *node) {
	n.starts++
	inst := &instance{node: n, label: fmt.Sprintf("%s#%d", n.name(), n.starts), wakeAt: w.now, resume: make(chan bool), fds: map[int32]int32{}}
	n.live, n.restartAt, n.pausedTo = inst, -1, 0
	ctx := context.WithValue(context.WithValue(w.ctx, worldKey{}, w), instKey{}, inst)
	cfg := w.moduleConfig(inst)
	go func() {
		if !<-inst.resume {
			w.events <- event{inst: inst, exit: true, err: errKilled}
			return
		}
		mod, err := w.rt.InstantiateModule(ctx, n.compiled, cfg)
		if err == nil {
			inst.preempt = newPreemption(mod, w.interleaver(inst))
			_, err = mod.ExportedFunction("_start").Call(ctx)
		}
		if mod != nil {
			mod.Close(context.Background())
		}
		w.events <- event{inst: inst, exit: true, err: err}
	}()
	w.trace.add(w.now, "start %s", inst.label)
}

const goPanicExitCode = 2

func (w *world) supervised(n *node, err error) bool {
	var ee *sys.ExitError
	return w.cfg.Supervise && n.crashable && !n.workload && errors.As(err, &ee) && ee.ExitCode() != 0 && ee.ExitCode() != goPanicExitCode
}

func exitedCleanly(err error) bool {
	var ee *sys.ExitError
	return err == nil || (errors.As(err, &ee) && ee.ExitCode() == 0)
}

func (w *world) resume(inst *instance) {
	w.now = max(w.now, inst.wakeAt)
	w.steps++
	watchdog := time.AfterFunc(time.Duration(w.cfg.Watchdog), w.cancel)
	defer watchdog.Stop()
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
			w.crashDisk(n, ev.inst.crashUntil, fmt.Sprintf("op/%d", n.ops))
		}
		if open := w.history.open(ev.inst.label); open > 0 {
			w.trace.add(w.now, "%s died with %d operations in flight, their outcome is unknown", ev.inst.label, open)
		}
	case w.ctx.Err() != nil:
		w.fail(Failure{Class: FailWatchdog, Node: n.name()}, fmt.Sprintf("watchdog: %s ran for more than %s of real time without yielding (livelock or busy loop)", ev.inst.label, time.Duration(w.cfg.Watchdog)))
	case n.workload && exitedCleanly(ev.err):
		n.done = true
		w.workloadDone(ev.inst)
	case n == w.finally && exitedCleanly(ev.err):
		w.finished = true
		w.trace.add(w.now, "finally check %s passed", ev.inst.label)
	case w.supervised(n, ev.err):
		n.restartAt = w.now + int64(uniform(w.sched, w.spec.RestartAfter))
		w.trace.add(w.now, "%s exited (%v), supervisor restarts it at %s", ev.inst.label, ev.err, fmtTime(n.restartAt))
	case exitedCleanly(ev.err):
		w.fail(Failure{Class: FailExit, Node: n.name()}, fmt.Sprintf("%s exited", ev.inst.label))
	default:
		w.fail(Failure{Class: FailCrash, Node: n.name()}, fmt.Sprintf("%s crashed: %v", ev.inst.label, ev.err))
	}
}

func (w *world) kill(inst *instance) {
	inst.killed = true
	inst.resume <- false
	w.handle(<-w.events)
}

func (w *world) applyFault(f *Fault) {
	if w.faultsSuppressed() {
		w.trace.add(w.now, "fault %s suppressed by %s", f.Kind, w.quietReason())
		return
	}
	switch f.Kind {
	case FaultCrash:
		n := w.nodeByName(f.Node)
		if n == nil || n.live == nil || !n.crashable {
			return
		}
		w.trace.add(w.now, "fault crash %s, restart at %s", n.live.label, fmtTime(int64(f.Until)))
		w.kill(n.live)
		w.crashDisk(n, int64(f.Until), fmt.Sprintf("at/%d", f.At))
	case FaultPause:
		n := w.nodeByName(f.Node)
		if n == nil || n.live == nil {
			return
		}
		n.pausedTo = int64(f.Until)
		w.trace.add(w.now, "fault pause %s until %s", n.live.label, fmtTime(n.pausedTo))
	case FaultClockJump:
		n := w.nodeByName(f.Node)
		if n == nil {
			return
		}
		n.skew += int64(f.Shift)
		if w.weakenedTo(MonotonicJump) {
			n.monoShift += int64(f.Shift)
		}
		w.trace.add(w.now, "fault %s's wall clock jumps %s", f.Node, fmtSigned(int64(f.Shift)))
	case FaultPartition:
		w.applyNetFault(*f)
		if len(f.Groups) > 0 {
			w.trace.add(w.now, "fault partition %s, heals at %s", fmtGroups(f.Groups), fmtTime(int64(f.Until)))
			return
		}
		w.trace.add(w.now, "fault partition %v | rest, heals at %s", f.Side, fmtTime(int64(f.Until)))
	case FaultClog:
		w.applyNetFault(*f)
		w.trace.add(w.now, "fault clog %s, lifts at %s", f.clogTarget(), fmtTime(int64(f.Until)))
	case FaultReset:
		n := w.nodeByName(f.Node)
		if n == nil || n.live == nil {
			return
		}
		w.trace.add(w.now, "fault reset %d connections of %s", w.net.reset(n.live), n.live.label)
	case FaultSlow:
		n := w.nodeByName(f.Node)
		if n == nil {
			return
		}
		n.slowUntil, n.slowFactor = int64(f.Until), f.Factor
		w.trace.add(w.now, "fault slow %s down %.1fx until %s", f.Node, f.Factor, fmtTime(n.slowUntil))
	case FaultBitrot:
		w.rot(f)
	}
}

func (w *world) crashDisk(n *node, until int64, crash string) {
	c := crashModel{torn: w.spec.TornWrites}
	if w.spec.ReorderWrites {
		c.pages = streams(w.seed, "writeback/"+n.name()+"/"+crash)
	}
	if w.spec.CorruptOnCrash {
		c.corrupt = streams(w.seed, "corrupt/"+n.name()+"/"+crash)
	}
	w.traceDamage(n, n.fs.crash(w.diskRand, c))
	n.restartAt = until
}

func (w *world) historyError(inst *instance, err error) {
	if err == nil {
		return
	}
	w.fail(Failure{Class: FailAssert, Node: inst.node.name()}, fmt.Sprintf("%s: history (model %s): %v", inst.label, w.cfg.Checker.Model, err))
	panic(errFailed)
}

func (w *world) checkHistory() {
	if w.failure != "" {
		w.lin = HistoryCheck{Model: w.history.model, Ops: w.history.finished()}
		return
	}
	w.lin = w.history.check()
	if !w.lin.Checked {
		return
	}
	w.trace.add(w.now, "checked %s", w.lin.Summary())
	for _, a := range w.lin.Anomalies {
		w.trace.add(w.now, "%s: %s", a.Kind, a.Explain)
	}
	if !w.lin.Ok {
		w.fail(w.lin.failure())
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
	for w.nextFault < len(w.faults) && observedAtRuntime(w.faults[w.nextFault].Kind) {
		w.nextFault++
	}
	if w.nextFault < len(w.faults) {
		steps = append(steps, step{at: int64(w.faults[w.nextFault].At), kind: stepFault})
	}
	for _, n := range w.nodes {
		switch {
		case n.live != nil:
			steps = append(steps, step{at: max(n.live.wakeAt, n.pausedTo), kind: stepResume, node: n})
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
	horizon := w.horizon
	for w.failure == "" && !w.finished {
		candidates := w.nextSteps()
		if len(candidates) == 0 {
			w.fail(Failure{Class: FailDeadlock}, "deadlock: every node has exited")
			return
		}
		if candidates[0].at == never {
			w.fail(Failure{Class: FailDeadlock}, "deadlock: every node is blocked with no timer or pending message")
			return
		}
		w.captureBefore(candidates[0].at)
		if w.takeBranch(candidates[0].at) {
			continue
		}
		if w.livenessDue(candidates[0].at) {
			continue
		}
		s := candidates[w.sched.IntN(len(candidates))]
		if s.at > horizon && w.deadline == never {
			w.trace.add(w.now, "horizon %s reached before the workload finished", fmtTime(horizon))
			return
		}
		switch s.kind {
		case stepFault:
			w.now = max(w.now, s.at)
			w.applyFault(&w.faults[w.nextFault])
			w.nextFault++
		case stepRestart:
			w.now = max(w.now, s.at)
			w.start(s.node)
		case stepResume:
			w.now = max(w.now, s.at)
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
