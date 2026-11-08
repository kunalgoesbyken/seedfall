package engine

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

type debugger struct {
	stop   func(w *world, next int64) bool
	cmds   chan func(*world) bool
	paused chan struct{}
	steps  int
	abort  bool
	over   bool
}

func (w *world) debugPoint(next int64) bool {
	d := w.debug
	if d == nil {
		return false
	}
	d.steps++
	if d.stop(w, next) {
		d.wait(w)
	}
	return d.abort
}

func (w *world) debugEnd() {
	if w.debug != nil {
		w.debug.over = true
		w.debug.wait(w)
	}
}

func (d *debugger) wait(w *world) {
	d.paused <- struct{}{}
	for cmd := range d.cmds {
		if !cmd(w) {
			return
		}
	}
}

func stopAfter(at int64) func(*world, int64) bool {
	return func(_ *world, next int64) bool { return next > at }
}

type Session struct {
	d    *debugger
	done chan Result
}

func (r *Runner) Debug(seed uint64, s Schedule, at time.Duration) (*Session, error) {
	if at < 0 {
		return nil, fmt.Errorf("debug time %s is negative", at)
	}
	s, explicit := r.schedule(seed, s)
	d := &debugger{stop: stopAfter(int64(at)), cmds: make(chan func(*world) bool), paused: make(chan struct{})}
	sess := &Session{d: d, done: make(chan Result, 1)}
	go func() { sess.done <- r.run(seed, s, runOptions{explicit: explicit, debug: d}) }()
	<-d.paused
	return sess, nil
}

func (s *Session) inspect(fn func(w *world)) {
	done := make(chan struct{})
	s.d.cmds <- func(w *world) bool {
		fn(w)
		close(done)
		return true
	}
	<-done
}

func (s *Session) resume(stop func(*world, int64) bool) bool {
	if s.d.over {
		return false
	}
	s.d.cmds <- func(*world) bool {
		s.d.stop = stop
		return false
	}
	<-s.d.paused
	return true
}

func (s *Session) Over() bool { return s.d.over }

func (s *Session) Step(n int) bool {
	target := s.d.steps + max(n, 1)
	return s.resume(func(*world, int64) bool { return s.d.steps >= target })
}

func (s *Session) RunTo(at time.Duration) bool {
	return s.resume(stopAfter(int64(at)))
}

func (s *Session) Until(re *regexp.Regexp) (string, bool) {
	var checked int
	var matched string
	s.inspect(func(w *world) { checked = len(w.trace.lines) })
	s.resume(func(w *world, _ int64) bool {
		for ; checked < len(w.trace.lines); checked++ {
			if re.MatchString(w.trace.lines[checked]) {
				matched = w.trace.lines[checked]
				return true
			}
		}
		return false
	})
	return matched, matched != ""
}

func (s *Session) Branch(key uint64) error {
	if s.d.over {
		return errors.New("the run is over")
	}
	var err error
	s.inspect(func(w *world) {
		if len(w.branches) > 0 {
			err = errors.New("this run has already branched; start a new session to branch again")
			return
		}
		w.branches = []Branch{{At: Duration(w.now), Key: key, reroll: true}}
	})
	return err
}

func (s *Session) Close() Result {
	if !s.d.over {
		s.d.cmds <- func(*world) bool {
			s.d.abort = true
			return false
		}
		<-s.d.paused
	}
	s.d.cmds <- func(*world) bool { return false }
	return <-s.done
}

type NodeState struct {
	Node     string        `json:"node"`
	Instance string        `json:"instance,omitempty"`
	State    string        `json:"state"`
	Until    time.Duration `json:"until,omitempty"`
}

type DebugState struct {
	At     time.Duration `json:"at"`
	Steps  int           `json:"steps"`
	Over   bool          `json:"over"`
	Failed bool          `json:"failed"`
	Reason string        `json:"reason,omitempty"`
	Nodes  []NodeState   `json:"nodes"`
}

func (s *Session) State() DebugState {
	var out DebugState
	s.inspect(func(w *world) {
		out = DebugState{At: time.Duration(w.now), Steps: s.d.steps, Over: s.d.over, Failed: w.failure != "", Reason: w.failure}
		for _, n := range w.nodes {
			out.Nodes = append(out.Nodes, w.nodeState(n))
		}
	})
	return out
}

func (w *world) nodeState(n *node) NodeState {
	st := NodeState{Node: n.name()}
	switch {
	case n.live != nil && n.pausedTo > w.now:
		st.Instance, st.State, st.Until = n.live.label, "paused", time.Duration(n.pausedTo)
	case n.live != nil && n.live.wakeAt == never:
		st.Instance, st.State = n.live.label, "blocked"
	case n.live != nil:
		st.Instance, st.State, st.Until = n.live.label, "runnable", time.Duration(max(n.live.wakeAt, w.now))
	case n.restartAt >= 0:
		st.State, st.Until = "down", time.Duration(n.restartAt)
	case n == w.finally:
		st.State = "not started"
	default:
		st.State = "exited"
	}
	return st
}

func (s *Session) Trace() []TraceLine {
	var out []TraceLine
	s.inspect(func(w *world) { out = slices.Clone(w.trace.entries) })
	return out
}

func (s *Session) Disk(name string) (Disk, error) {
	var d Disk
	err := s.onNode(name, func(w *world, n *node) {
		d = n.fs.snapshot(n.name())
		w.describeDisk(&d)
	})
	return d, err
}

func (s *Session) File(name, p string) (FileState, []byte, error) {
	var f FileState
	var live []byte
	found := false
	err := s.onNode(name, func(_ *world, n *node) {
		in, ok := n.fs.files[clean(p)]
		if !ok {
			return
		}
		found, live = true, slices.Clone(in.data)
		f = fileState(clean(p), in, n.fs.entryDurable(clean(p), in))
	})
	if err == nil && !found {
		err = fmt.Errorf("%s has no file %s", name, p)
	}
	return f, live, err
}

func (s *Session) onNode(name string, fn func(*world, *node)) error {
	err := fmt.Errorf("unknown node %q", name)
	s.inspect(func(w *world) {
		if n := w.nodeByName(name); n != nil {
			err = nil
			fn(w, n)
		}
	})
	return err
}

type InFlight struct {
	Conn    int32         `json:"conn"`
	Kind    string        `json:"kind"`
	From    string        `json:"from"`
	To      string        `json:"to"`
	Bytes   int           `json:"bytes,omitempty"`
	Sent    time.Duration `json:"sent"`
	Arrives time.Duration `json:"arrives"`
}

func (s *Session) InFlight() []InFlight {
	var out []InFlight
	s.inspect(func(w *world) { out = w.net.inflight() })
	return out
}

func (n *network) inflight() []InFlight {
	var out []InFlight
	for _, id := range n.ids() {
		switch h := n.handles[id].(type) {
		case *listener:
			for _, ep := range h.backlog {
				out = append(out, InFlight{Conn: ep.conn, Kind: MessageConnect, From: ep.peer.owner.label, To: h.owner.label, Arrives: time.Duration(ep.acceptAt)})
				out = ep.inflight(out)
			}
		case *endpoint:
			out = h.inflight(out)
		case *udpSocket:
			for _, d := range h.inbox {
				out = append(out, InFlight{Conn: d.sock, Kind: MessageDatagram, From: d.fromInst, To: h.owner.label, Bytes: len(d.data), Sent: time.Duration(d.sent), Arrives: time.Duration(d.at)})
			}
		}
	}
	return out
}

func (ep *endpoint) inflight(out []InFlight) []InFlight {
	for _, c := range ep.inbox {
		out = append(out, InFlight{Conn: ep.conn, Kind: chunkNames[c.kind], From: ep.peer.owner.label, To: ep.owner.label, Bytes: len(c.data), Sent: time.Duration(c.sent), Arrives: time.Duration(c.at)})
	}
	return out
}

func (s *Session) Faults() (active []Fault, next *Fault) {
	s.inspect(func(w *world) {
		for _, f := range mergeFaults(w.faults[:w.nextFault], w.ioFaults) {
			if int64(f.At) <= w.now && int64(f.Until) > w.now {
				active = append(active, f)
			}
		}
		if s.d.over {
			return
		}
		for _, f := range w.faults[w.nextFault:] {
			if !observedAtRuntime(f.Kind) {
				next = &f
				return
			}
		}
	})
	return active, next
}
