package engine

import (
	"strings"
	"testing"
)

func recorded(capture int64, calls ...hostCall) Result {
	h := newHostCalls(capture)
	for _, c := range calls {
		h.record(c)
	}
	return Result{HostCalls: h.n, hostHash: h.hash, checkpoints: h.checkpoints, captured: h.captured}
}

func clockReads(n int) []hostCall {
	calls := make([]hostCall, n)
	for i := range calls {
		calls[i] = hostCall{At: int64(i), Inst: "a#1", Name: "clock_nanotime"}
	}
	return calls
}

func TestHostCallHashIsStableAndCheckpointed(t *testing.T) {
	a, b := recorded(0, clockReads(2*hostCheckpoint+5)...), recorded(0, clockReads(2*hostCheckpoint+5)...)
	if len(a.checkpoints) != 2 || a.Hash() != b.Hash() || a.HostCalls != 2*hostCheckpoint+5 {
		t.Fatalf("%d checkpoints, hashes %s and %s", len(a.checkpoints), a.Hash(), b.Hash())
	}
	if _, diverged := firstHostWindow(a, b); diverged {
		t.Fatal("identical call sequences diverged")
	}
	if recorded(0, hostCall{Name: "ab", Inst: "c"}).Hash() == recorded(0, hostCall{Name: "a", Inst: "bc"}).Hash() {
		t.Fatal("field boundaries do not change the hash")
	}
}

func TestHostDivergenceNamesTheFirstDifferingCall(t *testing.T) {
	calls := clockReads(3 * hostCheckpoint)
	other := append([]hostCall(nil), calls...)
	other[hostCheckpoint+9] = hostCall{At: int64(hostCheckpoint + 9), Inst: "a#1", Name: "random_get", Arg: 8}
	window, diverged := firstHostWindow(recorded(0, calls...), recorded(0, other...))
	if !diverged || window != 2 {
		t.Fatalf("window %d, diverged %v", window, diverged)
	}
	a, b := recorded(window, calls...), recorded(window, other...)
	if len(a.captured) != hostCheckpoint {
		t.Fatalf("captured %d calls", len(a.captured))
	}
	got := hostDivergence(window, a, b)
	want := "host call 4106: a#1 clock_nanotime(0) at 0.000s vs a#1 random_get(8) at 0.000s, after a#1 clock_nanotime(0) at 0.000s; host calls 4097-8192"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("divergence %q, want prefix %q", got, want)
	}
}

func TestHostDivergenceInTheLastPartialWindow(t *testing.T) {
	calls := clockReads(hostCheckpoint + 3)
	window, diverged := firstHostWindow(recorded(0, calls...), recorded(0, calls[:hostCheckpoint+1]...))
	if !diverged || window != 2 {
		t.Fatalf("window %d, diverged %v", window, diverged)
	}
	got := hostDivergence(window, recorded(window, calls[:hostCheckpoint+1]...), recorded(window, calls...))
	if !strings.HasPrefix(got, "host call 4098: <end of run> vs a#1 clock_nanotime(0)") {
		t.Fatalf("divergence %q", got)
	}
}
