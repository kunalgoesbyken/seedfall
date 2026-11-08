package engine

import (
	"slices"
	"testing"
)

func live(name string, wakeAt, pausedTo int64) *node {
	n := &node{spec: NodeSpec{Name: name}, restartAt: -1, pausedTo: pausedTo}
	n.live = &instance{node: n, wakeAt: wakeAt}
	return n
}

func down(name string, restartAt int64) *node {
	return &node{spec: NodeSpec{Name: name}, restartAt: restartAt}
}

func stepNames(steps []step) []string {
	var out []string
	for _, s := range steps {
		name := "fault"
		if s.node != nil {
			name = s.node.name()
		}
		out = append(out, name)
	}
	return out
}

func TestNextStepsBreaksTiesByTimeThenKind(t *testing.T) {
	tests := []struct {
		name   string
		faults []Fault
		nodes  []*node
		at     int64
		kind   int
		want   []string
	}{
		{"the earliest step wins", nil, []*node{live("a", 30, 0), live("b", 10, 0), down("c", 20)}, 10, stepResume, []string{"b"}},
		{"a fault goes before a restart and a resume at the same time", []Fault{{At: 10}}, []*node{live("a", 10, 0), down("b", 10)}, 10, stepFault, []string{"fault"}},
		{"a restart goes before a resume at the same time", nil, []*node{live("a", 10, 0), down("b", 10)}, 10, stepRestart, []string{"b"}},
		{"every resume due at the same time is a candidate", nil, []*node{live("a", 10, 0), live("b", 20, 0), live("c", 10, 0)}, 10, stepResume, []string{"a", "c"}},
		{"a paused node waits for the pause to end", nil, []*node{live("a", 5, 40), live("b", 30, 0)}, 30, stepResume, []string{"b"}},
		{"a node that exited is never scheduled", nil, []*node{{spec: NodeSpec{Name: "a"}, restartAt: -1}, live("b", 50, 0)}, 50, stepResume, []string{"b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &world{faults: tt.faults, nodes: tt.nodes}
			got := w.nextSteps()
			for _, s := range got {
				if s.at != tt.at || s.kind != tt.kind {
					t.Fatalf("candidate %+v, want every candidate at %d with kind %d", s, tt.at, tt.kind)
				}
			}
			if names := stepNames(got); !slices.Equal(names, tt.want) {
				t.Fatalf("candidates %v, want %v", names, tt.want)
			}
		})
	}
}

func TestNextStepsIsEmptyWhenNothingCanRun(t *testing.T) {
	w := &world{faults: []Fault{{At: 1}}, nextFault: 1, nodes: []*node{{spec: NodeSpec{Name: "a"}, restartAt: -1}}}
	if got := w.nextSteps(); got != nil {
		t.Fatalf("nextSteps = %v, want nil", stepNames(got))
	}
}
