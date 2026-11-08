package engine

import (
	"fmt"
	"io"
	"math/bits"
	"math/rand/v2"
	"slices"
	"time"
)

const (
	streamRandom = "random"
	branchMix    = 0x9e3779b97f4a7c15
)

var (
	replayedStreams = []string{"sched", "net", "disk", "disklatency", streamRandom}
	BranchStreams   = append(slices.Clone(replayedStreams), "io", "udp", "mutant")
)

type Branch struct {
	At     Duration `json:"at"`
	Key    uint64   `json:"key"`
	Stream string   `json:"stream,omitempty"`
	reroll bool
}

func (b Branch) covers(stream string) bool { return b.Stream == "" || b.Stream == stream }

func replayable(branches []Branch) []Branch {
	out := slices.Clone(branches)
	for i := range out {
		out[i].reroll = false
	}
	return out
}

func (w *world) streamSeed(stream string) uint64 {
	seed := w.seed
	for i, b := range w.branches[:w.taken] {
		if b.covers(stream) {
			seed ^= bits.RotateLeft64(b.Key*branchMix, i)
		}
	}
	return seed
}

func (w *world) takeBranch(nextStep int64) bool {
	if w.taken >= len(w.branches) || nextStep < int64(w.branches[w.taken].At) {
		return false
	}
	b := w.branches[w.taken]
	w.taken++
	for _, s := range []struct {
		name string
		rand **rand.Rand
	}{{"sched", &w.sched}, {"net", &w.netRand}, {"disk", &w.diskRand}, {"io", &w.ioRand}, {"udp", &w.udpRand}, {"disklatency", &w.latRand}, {"mutant", &w.mutantRand}} {
		if b.covers(s.name) {
			*s.rand = streams(w.streamSeed(s.name), s.name)
		}
	}
	if b.Stream != "" {
		w.trace.add(w.now, "branch %d taken at %s: the %s stream is re-keyed", b.Key, fmtTime(int64(b.At)), b.Stream)
		return true
	}
	clear(w.bugs.fire)
	if b.reroll {
		future := slices.DeleteFunc(generateFaults(w.cfg, w.spec, streams(w.streamSeed("faults"), "faults")), func(f Fault) bool { return f.At < b.At })
		w.faults = append(slices.Clip(w.faults[:w.nextFault]), future...)
		w.explicit = false
	}
	w.trace.add(w.now, "branch %d taken at %s: the future is re-randomized", b.Key, fmtTime(int64(b.At)))
	return true
}

type guestRandom struct {
	w       *world
	inst    *instance
	name    string
	current io.Reader
	seed    uint64
}

func (w *world) guestRandom(inst *instance, name string) *guestRandom {
	return &guestRandom{w: w, inst: inst, name: name, current: w.chacha(name), seed: w.streamSeed(streamRandom)}
}

func (w *world) chacha(name string) io.Reader {
	key := [32]byte{}
	r := streams(w.streamSeed(streamRandom), name)
	for i := range key {
		key[i] = byte(r.Uint32())
	}
	return rand.NewChaCha8(key)
}

func (g *guestRandom) Read(p []byte) (int, error) {
	g.w.hostCall(g.inst, "random_get", "", int64(len(p)))
	if seed := g.w.streamSeed(streamRandom); seed != g.seed {
		g.current, g.seed = g.w.chacha(g.name), seed
	}
	return g.current.Read(p)
}

func (r *Runner) Branch(seed uint64, at time.Duration, key uint64) Result {
	started := time.Now()
	res := r.run(seed, Schedule{Faults: r.Faults(seed), Branches: []Branch{{At: Duration(at), Key: key, reroll: true}}}, runOptions{})
	res.Elapsed = time.Since(started)
	return res
}

func checkBranches(branches []Branch) []string {
	var out []string
	for i, b := range branches {
		if b.At < 0 {
			out = append(out, fmt.Sprintf("branch %d: at is negative", i+1))
		}
		if i > 0 && b.At < branches[i-1].At {
			out = append(out, fmt.Sprintf("branch %d: at %s is before the previous branch", i+1, time.Duration(b.At)))
		}
		if b.Stream != "" && !slices.Contains(BranchStreams, b.Stream) {
			out = append(out, fmt.Sprintf("branch %d: unknown stream %q, want one of %v", i+1, b.Stream, BranchStreams))
		}
	}
	return out
}
