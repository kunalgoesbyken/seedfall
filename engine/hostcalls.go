package engine

import (
	"fmt"
	"slices"
)

const (
	hostCheckpoint = 4096
	fnvOffset      = uint64(14695981039346656037)
	fnvPrime       = uint64(1099511628211)
)

type hostCall struct {
	N      int64
	At     int64
	Inst   string
	Name   string
	Target string
	Arg    int64
}

func (c hostCall) String() string {
	call := c.Name
	if c.Target != "" {
		call += " " + c.Target
	}
	return fmt.Sprintf("%s %s(%d) at %s", c.Inst, call, c.Arg, fmtTime(c.At))
}

type hostCalls struct {
	hash        uint64
	n           int64
	checkpoints []uint64
	capture     int64
	captured    []hostCall
}

func newHostCalls(capture int64) hostCalls { return hostCalls{hash: fnvOffset, capture: capture} }

func fnvString(h uint64, s string) uint64 {
	for i := range len(s) {
		h = (h ^ uint64(s[i])) * fnvPrime
	}
	return (h ^ 0xff) * fnvPrime
}

func fnvInt(h uint64, v int64) uint64 {
	for i := range 8 {
		h = (h ^ uint64(byte(v>>(8*i)))) * fnvPrime
	}
	return h
}

func hostWindow(n int64) int64 { return (n-1)/hostCheckpoint + 1 }

func (h *hostCalls) record(c hostCall) {
	h.hash = fnvInt(fnvInt(fnvString(fnvString(fnvString(h.hash, c.Name), c.Inst), c.Target), c.Arg), c.At)
	h.n++
	c.N = h.n
	if hostWindow(h.n) == h.capture {
		h.captured = append(h.captured, c)
	}
	if h.n%hostCheckpoint == 0 {
		h.checkpoints = append(h.checkpoints, h.hash)
	}
}

func (w *world) hostCall(inst *instance, name, target string, arg int64) {
	label := ""
	if inst != nil {
		label = inst.label
	}
	w.calls.record(hostCall{At: w.now, Inst: label, Name: name, Target: target, Arg: arg})
}

func boolInt(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func (r Result) Hash() string { return fmt.Sprintf("%016x", r.hostHash) }

func firstHostWindow(a, b Result) (int64, bool) {
	for i := range min(len(a.checkpoints), len(b.checkpoints)) {
		if a.checkpoints[i] != b.checkpoints[i] {
			return int64(i) + 1, true
		}
	}
	if a.HostCalls == b.HostCalls && a.hostHash == b.hostHash {
		return 0, false
	}
	return int64(min(len(a.checkpoints), len(b.checkpoints))) + 1, true
}

func hostDivergence(window int64, a, b Result) string {
	first := (window-1)*hostCheckpoint + 1
	span := fmt.Sprintf("host calls %d-%d (the runs made %d and %d host calls, final hashes %s and %s)", first, window*hostCheckpoint, a.HostCalls, b.HostCalls, a.Hash(), b.Hash())
	i := slices.IndexFunc(a.captured, func(c hostCall) bool {
		j := int(c.N - first)
		return j >= len(b.captured) || b.captured[j] != c
	})
	switch {
	case i > 0:
		return fmt.Sprintf("host call %d: %s vs %s, after %s; %s", a.captured[i].N, a.captured[i], lineCall(b.captured, i), a.captured[i-1], span)
	case i == 0:
		return fmt.Sprintf("host call %d: %s vs %s; %s", a.captured[0].N, a.captured[0], lineCall(b.captured, 0), span)
	case len(b.captured) > len(a.captured):
		return fmt.Sprintf("host call %d: <end of run> vs %s; %s", b.captured[len(a.captured)].N, b.captured[len(a.captured)], span)
	}
	return span
}

func lineCall(calls []hostCall, i int) string {
	if i < len(calls) {
		return calls[i].String()
	}
	return "<end of run>"
}
