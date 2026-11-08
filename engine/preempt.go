package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/tetratelabs/wazero/api"
)

const (
	preemptCalls    = 1 << 22
	interleaveCalls = 1 << 12
	pctSteps        = 1 << 20
	preemptSlice    = int64(10 * time.Millisecond)

	goStackguard0 = 16
	goGM          = 48
	stackPreempt  = 0xfffffffffffffade
)

const (
	StrategyNone   = "none"
	StrategyRandom = "random"
	StrategyPCT    = "pct"
	StrategyCalls  = "calls"
)

var PreemptStrategies = []string{StrategyNone, StrategyRandom, StrategyPCT, StrategyCalls}

func (p *PreemptSpec) normalize() error {
	if p.Strategy == "" {
		p.Strategy = StrategyNone
	}
	if p.Interleave {
		if p.Strategy != StrategyNone && p.Strategy != StrategyRandom {
			return fmt.Errorf("interleave is shorthand for strategy %s, but strategy is %s", StrategyRandom, p.Strategy)
		}
		p.Strategy = StrategyRandom
	}
	switch {
	case !slices.Contains(PreemptStrategies, p.Strategy):
		return fmt.Errorf("unknown strategy %q, want one of %s", p.Strategy, strings.Join(PreemptStrategies, ", "))
	case p.Strategy == StrategyPCT && p.Depth < 1:
		return fmt.Errorf("depth %d is below 1", p.Depth)
	case p.Strategy == StrategyPCT && p.Steps < 1:
		return fmt.Errorf("steps %d is below 1", p.Steps)
	case p.Strategy == StrategyCalls && (p.Calls < 1 || p.Calls > preemptCalls):
		return fmt.Errorf("calls %d is outside 1 to %d, the node's time slice", p.Calls, preemptCalls)
	}
	return nil
}

func (p PreemptSpec) describe() string {
	unit := "function calls"
	if p.Loops {
		unit = "function calls and loop iterations"
	}
	switch p.Strategy {
	case StrategyRandom:
		return fmt.Sprintf("preempt: random, the running goroutine is preempted after a seeded 1 to %d counted %s", interleaveCalls, unit)
	case StrategyPCT:
		return fmt.Sprintf("preempt: pct, depth %d, change points per node: %d, drawn over the first %d counted %s", p.Depth, p.Depth-1, p.Steps, unit)
	case StrategyCalls:
		return fmt.Sprintf("preempt: calls, the running goroutine is preempted every %d counted %s", p.Calls, unit)
	}
	if p.Loops {
		return fmt.Sprintf("preempt: none, a node is preempted after %d counted %s", preemptCalls, unit)
	}
	return ""
}

type preemption struct {
	calls      api.MutableGlobal
	g          api.Global
	mem        api.Memory
	node       *node
	interleave *rand.Rand
	every      int64
	armed      int64
	spun       int64
}

func (w *world) newPreemption(mod api.Module, inst *instance) *preemption {
	calls, _ := mod.ExportedGlobal(callsExport).(api.MutableGlobal)
	g := mod.ExportedGlobal(gExport)
	if calls == nil || g == nil {
		return nil
	}
	p := &preemption{calls: calls, g: g, mem: mod.Memory(), node: inst.node}
	switch w.cfg.Preempt.Strategy {
	case StrategyRandom:
		p.interleave = streams(w.seed, "interleave/"+inst.label)
	case StrategyCalls:
		p.every = w.cfg.Preempt.Calls
	}
	p.reset()
	return p
}

func (p *preemption) reset() {
	if p != nil {
		p.spun = 0
		p.arm()
	}
}

func (p *preemption) arm() {
	switch n := p.node; {
	case p.interleave != nil:
		p.armed = 1 + p.interleave.Int64N(interleaveCalls)
	case p.every > 0:
		p.armed = p.every
	case len(n.changes) > 0:
		p.armed = min(n.changes[0]-n.calls, preemptCalls-p.spun)
	default:
		p.armed = preemptCalls - p.spun
	}
	p.calls.Set(uint64(p.armed))
}

func (p *preemption) settle() {
	if p != nil {
		ran := p.armed - int64(int32(p.calls.Get()))
		p.node.calls += ran
		p.armed -= ran
	}
}

func (p *preemption) due() bool { return p != nil && int32(p.calls.Get()) <= 0 }

func (p *preemption) preemptGoroutine() {
	g := uint32(p.g.Get())
	m, _ := p.mem.ReadUint64Le(g + goGM)
	g0, _ := p.mem.ReadUint64Le(uint32(m))
	if g != 0 && m != 0 && uint64(g) != g0 {
		p.mem.WriteUint64Le(g+goStackguard0, stackPreempt)
	}
}

func (w *world) schedYield(ctx context.Context, m api.Module, stack []uint64) {
	stack[0] = errnoSuccess
	inst := w.instance(ctx)
	p := inst.preempt
	if !p.due() {
		return
	}
	p.spun += p.armed
	p.settle()
	p.preemptGoroutine()
	w.changePoint(inst)
	if p.spun < preemptCalls {
		p.arm()
		return
	}
	w.park(inst, w.now+w.stretch(preemptSlice, inst.node))
}

func (w *world) changePoint(inst *instance) {
	n := inst.node
	if len(n.changes) == 0 || n.changes[0] != n.calls {
		return
	}
	n.changes = n.changes[1:]
	n.fired++
	w.ioFaults = append(w.ioFaults, Fault{At: Duration(w.now), Kind: FaultPreempt, Node: n.name(), Op: n.calls})
	w.trace.about(w.now, instLine(inst), "fault preempt %s's running goroutine at counted call %d", inst.label, n.calls)
}

func (w *world) planChanges(n *node) {
	spec := w.cfg.Preempt
	if spec.Strategy != StrategyPCT {
		return
	}
	if w.explicit {
		for _, f := range w.faults {
			if f.Kind == FaultPreempt && f.Node == n.name() {
				n.changes = append(n.changes, f.Op)
			}
		}
	} else {
		r := streams(w.seed, "pct/"+n.name())
		for range spec.Depth - 1 {
			n.changes = append(n.changes, 1+r.Int64N(spec.Steps))
		}
	}
	n.changes = slices.DeleteFunc(n.changes, func(op int64) bool { return op < 1 })
	slices.Sort(n.changes)
	n.changes = slices.Compact(n.changes)
}

func (w *world) preemptSummary() {
	if w.cfg.Preempt.Strategy != StrategyPCT {
		return
	}
	for _, n := range w.nodes {
		if n.starts > 0 {
			w.trace.about(w.now, nodeLine(n.name()), "pct: %s ran %d counted calls, change points fired: %d", n.name(), n.calls, n.fired)
		}
	}
}
