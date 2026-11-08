package engine

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/tetratelabs/wazero/api"
)

const (
	preemptCalls    = 1 << 22
	interleaveCalls = 1 << 12
	preemptSlice    = int64(10 * time.Millisecond)

	goStackguard0 = 16
	goGM          = 48
	stackPreempt  = 0xfffffffffffffade
)

type preemption struct {
	calls      api.MutableGlobal
	g          api.Global
	mem        api.Memory
	interleave *rand.Rand
	armed      int64
	spun       int64
}

func newPreemption(mod api.Module, interleave *rand.Rand) *preemption {
	calls, _ := mod.ExportedGlobal(callsExport).(api.MutableGlobal)
	g := mod.ExportedGlobal(gExport)
	if calls == nil || g == nil {
		return nil
	}
	p := &preemption{calls: calls, g: g, mem: mod.Memory(), interleave: interleave}
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
	p.armed = preemptCalls
	if p.interleave != nil {
		p.armed = 1 + p.interleave.Int64N(interleaveCalls)
	}
	p.calls.Set(uint64(p.armed))
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
	p := w.instance(ctx).preempt
	if !p.due() {
		return
	}
	p.preemptGoroutine()
	p.spun += p.armed
	if p.spun < preemptCalls {
		p.arm()
		return
	}
	w.park(w.instance(ctx), w.now+preemptSlice)
}

func (w *world) interleaver(inst *instance) *rand.Rand {
	if !w.cfg.Preempt.Interleave {
		return nil
	}
	return streams(w.seed, "interleave/"+inst.label)
}
