package engine

import (
	"fmt"
	"slices"
	"testing"
)

type fakeReplayer struct {
	fails func(present map[Duration]bool) (Failure, string)
	runs  int
}

func (f *fakeReplayer) Run(seed uint64, faults []Fault) Result {
	f.runs++
	present := map[Duration]bool{}
	for _, fault := range faults {
		present[fault.At] = true
	}
	res := Result{Seed: seed, Faults: faults}
	res.Failure, res.Reason = f.fails(present)
	res.Failed = res.Reason != ""
	return res
}

func schedule(n int) []Fault {
	out := make([]Fault, n)
	for i := range out {
		out[i] = Fault{At: Duration(i + 1), Kind: FaultCrash, Node: "n1"}
	}
	return out
}

func lost(n int) (Failure, string) {
	return Failure{Class: FailAssert, Node: "client"}, fmt.Sprintf("acknowledged write lost: k%d", n)
}

func TestShrinkReturnsAOneMinimalScheduleWithTheSameFailure(t *testing.T) {
	tests := []struct {
		name  string
		fails func(map[Duration]bool) (Failure, string)
		want  int
	}{
		{"two faults are both needed", func(p map[Duration]bool) (Failure, string) {
			if p[2] && p[5] {
				return lost(len(p))
			}
			return Failure{}, ""
		}, 2},
		{"any two of three faults are enough", func(p map[Duration]bool) (Failure, string) {
			if n := btoi(p[1]) + btoi(p[3]) + btoi(p[6]); n >= 2 {
				return lost(n)
			}
			return Failure{}, ""
		}, 2},
		{"a different failure is not accepted", func(p map[Duration]bool) (Failure, string) {
			switch {
			case p[4] && p[7]:
				return lost(len(p))
			case p[4]:
				return Failure{Class: FailCrash, Node: "n1"}, "n1 crashed: panic"
			}
			return Failure{}, ""
		}, 2},
		{"a failure with no faults shrinks to nothing", func(p map[Duration]bool) (Failure, string) {
			return lost(len(p))
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeReplayer{fails: tt.fails}
			original := f.Run(1, schedule(8))
			if !original.Failed {
				t.Fatal("the full schedule must fail")
			}
			var progress []int
			best := Shrink(f, original, func(n int) { progress = append(progress, n) })
			if len(best.Faults) != tt.want || !best.sameFailure(original) {
				t.Fatalf("shrunk to %d faults (%q), want %d with the original failure", len(best.Faults), best.Reason, tt.want)
			}
			if !slices.IsSortedFunc(progress, func(a, b int) int { return b - a }) {
				t.Fatalf("progress %v is not decreasing", progress)
			}
			for i := range best.Faults {
				without := slices.Delete(slices.Clone(best.Faults), i, i+1)
				if f.Run(best.Seed, without).sameFailure(original) {
					t.Fatalf("removing fault %v still fails the same way, so %v is not 1-minimal", best.Faults[i], best.Faults)
				}
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestSameFailureIgnoresNumbersOnly(t *testing.T) {
	base := Result{Failed: true, Failure: Failure{Class: FailAssert, Node: "client"}, Reason: "lost k12 after 3 crashes"}
	tests := []struct {
		name  string
		other Result
		want  bool
	}{
		{"different numbers", Result{Failed: true, Failure: base.Failure, Reason: "lost k7 after 41 crashes"}, true},
		{"different words", Result{Failed: true, Failure: base.Failure, Reason: "stale k12 after 3 crashes"}, false},
		{"different node", Result{Failed: true, Failure: Failure{Class: FailAssert, Node: "server"}, Reason: base.Reason}, false},
		{"different class", Result{Failed: true, Failure: Failure{Class: FailCrash, Node: "client"}, Reason: base.Reason}, false},
		{"passing run", Result{Failure: base.Failure, Reason: base.Reason}, false},
	}
	for _, tt := range tests {
		if got := tt.other.sameFailure(base); got != tt.want {
			t.Errorf("%s: sameFailure = %v, want %v", tt.name, got, tt.want)
		}
	}
}
