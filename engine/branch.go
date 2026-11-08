package engine

import (
	"io"
	"math/rand/v2"
	"slices"
	"time"
)

type branch struct {
	at    int64
	key   uint64
	taken bool
}

func (w *world) streamSeed() uint64 {
	if w.branch == nil || !w.branch.taken {
		return w.seed
	}
	return w.seed ^ (w.branch.key * 0x9e3779b97f4a7c15)
}

func (w *world) takeBranch(nextStep int64) bool {
	b := w.branch
	if b == nil || b.taken || nextStep < b.at {
		return false
	}
	b.taken = true
	seed := w.streamSeed()
	w.sched, w.netRand, w.diskRand, w.ioRand, w.latRand = streams(seed, "sched"), streams(seed, "net"), streams(seed, "disk"), streams(seed, "io"), streams(seed, "disklatency")
	clear(w.bugs.fire)
	future := slices.DeleteFunc(generateFaults(w.cfg, w.spec, streams(seed, "faults")), func(f Fault) bool { return int64(f.At) < b.at })
	w.faults = append(slices.Clip(w.faults[:w.nextFault]), future...)
	w.trace.add(w.now, "branch %d taken at %s: the future is re-randomized", b.key, fmtTime(b.at))
	return true
}

type guestRandom struct {
	w        *world
	inst     *instance
	name     string
	current  io.Reader
	branched bool
}

func (w *world) guestRandom(inst *instance, name string) *guestRandom {
	return &guestRandom{w: w, inst: inst, name: name, current: w.chacha(name)}
}

func (w *world) chacha(name string) io.Reader {
	key := [32]byte{}
	r := streams(w.streamSeed(), name)
	for i := range key {
		key[i] = byte(r.Uint32())
	}
	return rand.NewChaCha8(key)
}

func (g *guestRandom) Read(p []byte) (int, error) {
	g.w.hostCall(g.inst, "random_get", "", int64(len(p)))
	if !g.branched && g.w.branch != nil && g.w.branch.taken {
		g.current, g.branched = g.w.chacha(g.name), true
	}
	return g.current.Read(p)
}

func (r *Runner) Branch(seed uint64, at time.Duration, key uint64) Result {
	started := time.Now()
	res := r.run(seed, Schedule{Faults: r.Faults(seed)}, false, &branch{at: int64(at), key: key}, nil, 0)
	res.Elapsed = time.Since(started)
	return res
}
