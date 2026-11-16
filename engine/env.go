package engine

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

type EnvNode struct {
	Node     string   `json:"node"`
	Instance string   `json:"instance,omitempty"`
	State    string   `json:"state"`
	Until    Duration `json:"until,omitempty"`
	Args     []string `json:"args,omitempty"`
	Starts   int      `json:"starts"`
}

type ClientStats struct {
	Invoked int `json:"invoked"`
	Ok      int `json:"ok"`
	Failed  int `json:"failed"`
	Unknown int `json:"unknown"`
	Open    int `json:"open"`
}

type Observation struct {
	At       Duration    `json:"at"`
	Over     bool        `json:"over"`
	Failed   bool        `json:"failed"`
	Reason   string      `json:"reason,omitempty"`
	Nodes    []EnvNode   `json:"nodes"`
	Log      []string    `json:"log"`
	InFlight int         `json:"in_flight"`
	Client   ClientStats `json:"client"`
}

func (r *Runner) Operate(seed uint64, s Schedule) (*Session, error) {
	if len(s.Operator) > 0 {
		return nil, errors.New("an operated session takes its actions from Do, not from the schedule")
	}
	return r.session(seed, s, &debugger{stop: stopAfter(0), gate: true})
}

func (s *Session) Do(op Op) error {
	if !s.d.gate {
		return errors.New("this session takes no operator actions")
	}
	if s.d.over {
		return errors.New("the run is over")
	}
	var err error
	s.inspect(func(w *world) {
		op.At = Duration(s.d.clock)
		if err = w.cfg.checkOps([]Op{op}, time.Duration(w.horizon)); err != nil {
			return
		}
		w.ops = append(w.ops, op)
		w.operate(op)
		w.nextOp = len(w.ops)
	})
	return err
}

func (s *Session) Advance(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("advance %s is not positive", d)
	}
	if err := s.Do(Op{Kind: OpWait, Args: []string{d.String()}}); err != nil {
		return err
	}
	s.d.clock += int64(d)
	s.resume(stopAfter(s.d.clock))
	return nil
}

func (s *Session) Ops() []Op {
	var out []Op
	s.inspect(func(w *world) { out = slices.Clone(w.ops[:w.nextOp]) })
	return out
}

func (s *Session) Peek() Observation {
	var out Observation
	s.inspect(func(w *world) { out = s.observation(w) })
	return out
}

func (s *Session) Observe() Observation {
	var out Observation
	s.inspect(func(w *world) {
		out = s.observation(w)
		out.Log = slices.Clone(w.trace.lines[min(s.seen, len(w.trace.lines)):])
		s.seen = len(w.trace.lines)
	})
	return out
}

func (s *Session) observation(w *world) Observation {
	out := Observation{At: Duration(w.now), Over: s.d.over, Failed: w.failure != "", Reason: w.failure, Client: w.history.stats(), InFlight: len(w.net.inflight())}
	for _, n := range w.nodes {
		st := w.nodeState(n)
		out.Nodes = append(out.Nodes, EnvNode{Node: st.Node, Instance: st.Instance, State: st.State, Until: Duration(st.Until), Args: slices.Clone(n.spec.Args), Starts: n.starts})
	}
	return out
}

func (s *Session) Finish() Result {
	s.resume(func(*world, int64) bool { return false })
	return s.Close()
}

func (h *history) stats() ClientStats {
	st := ClientStats{Invoked: len(h.ops)}
	for _, op := range h.ops {
		switch {
		case op.End < 0:
			st.Open++
		case op.Status == StatusOk:
			st.Ok++
		case op.Status == StatusFail:
			st.Failed++
		default:
			st.Unknown++
		}
	}
	return st
}
