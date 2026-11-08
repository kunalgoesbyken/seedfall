package engine

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

const (
	MessageConnect = "connect"
	MessageData    = "data"
	MessageEOF     = "eof"
	MessageReset   = "reset"

	MarkEvent    = "event"
	MarkProperty = "property"
	MarkFailure  = "failure"

	unread = time.Duration(-1)
)

var chunkNames = []string{chunkData: MessageData, chunkEOF: MessageEOF, chunkReset: MessageReset}

type Message struct {
	Conn         int32         `json:"conn"`
	Kind         string        `json:"kind"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	FromInstance string        `json:"from_instance"`
	ToInstance   string        `json:"to_instance"`
	Bytes        int           `json:"bytes,omitempty"`
	Sent         time.Duration `json:"sent"`
	Arrives      time.Duration `json:"arrives"`
	Read         time.Duration `json:"read"`
	Held         bool          `json:"held,omitempty"`
	Duplicate    bool          `json:"duplicate,omitempty"`
}

func (m Message) Delivered() bool { return m.Read >= 0 }

type Life struct {
	Node     string        `json:"node"`
	Instance string        `json:"instance"`
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Ended    string        `json:"ended,omitempty"`
}

type Mark struct {
	At       time.Duration `json:"at"`
	Node     string        `json:"node,omitempty"`
	Instance string        `json:"instance,omitempty"`
	Kind     string        `json:"kind"`
	Text     string        `json:"text"`
}

type TimelineOp struct {
	HistoryOp
	Node     string `json:"node"`
	Instance string `json:"instance"`
}

type FaultSpan struct {
	Kind  string        `json:"kind"`
	Nodes []string      `json:"nodes,omitempty"`
	Start time.Duration `json:"start"`
	End   time.Duration `json:"end"`
	Text  string        `json:"text"`
}

type Timeline struct {
	Seed     uint64        `json:"seed"`
	Nodes    []string      `json:"nodes"`
	End      time.Duration `json:"end"`
	Lives    []Life        `json:"lives"`
	Messages []Message     `json:"messages"`
	Ops      []TimelineOp  `json:"ops"`
	Marks    []Mark        `json:"marks"`
	Faults   []FaultSpan   `json:"faults"`
	Logs     []TraceLine   `json:"logs"`
}

type recorder struct {
	messages []Message
	lives    []Life
	marks    []Mark
}

func (r *recorder) started(inst *instance, now int64) {
	if r == nil {
		return
	}
	r.lives = append(r.lives, Life{Node: inst.node.name(), Instance: inst.label, Start: time.Duration(now), End: unread})
}

func (r *recorder) ended(inst *instance, now int64, err error) {
	if r == nil {
		return
	}
	i := slices.IndexFunc(r.lives, func(l Life) bool { return l.Instance == inst.label && l.End < 0 })
	if i < 0 {
		return
	}
	r.lives[i].End = time.Duration(now)
	switch {
	case inst.killed:
		r.lives[i].Ended = "killed"
	case exitedCleanly(err):
		r.lives[i].Ended = "exited"
	default:
		r.lives[i].Ended = err.Error()
	}
}

func (r *recorder) mark(inst *instance, now int64, kind, text string) {
	if r == nil {
		return
	}
	r.marks = append(r.marks, Mark{At: time.Duration(now), Node: inst.node.name(), Instance: inst.label, Kind: kind, Text: text})
}

func (r *recorder) property(inst *instance, now int64, kind PropertyKind, cond bool, msg string, p *Property) {
	switch {
	case kind.needsWitness() && cond && p.True == 1:
		r.mark(inst, now, MarkProperty, fmt.Sprintf("%s first true: %s", kind, msg))
	case !kind.needsWitness() && kind.mustBeReached() && p.Hits == 1:
		r.mark(inst, now, MarkProperty, fmt.Sprintf("%s first reached: %s", kind, msg))
	}
}

func (r *recorder) message(m Message) int {
	if r == nil {
		return 0
	}
	r.messages = append(r.messages, m)
	return len(r.messages)
}

func (r *recorder) read(id int, now int64) {
	if r == nil || id == 0 || r.messages[id-1].Read >= 0 {
		return
	}
	r.messages[id-1].Read = time.Duration(now)
}

func (w *world) timeline(res Result) *Timeline {
	t := &Timeline{Seed: res.Seed, End: res.Duration, Lives: w.rec.lives, Messages: w.rec.messages, Marks: w.rec.marks, Logs: w.trace.entries}
	for _, n := range w.nodes {
		t.Nodes = append(t.Nodes, n.name())
	}
	for i, op := range res.History.Ops {
		owner := w.history.owners[i]
		t.Ops = append(t.Ops, TimelineOp{HistoryOp: op, Instance: owner, Node: w.instanceNode(owner)})
	}
	if res.Failed {
		t.Marks = append(t.Marks, Mark{At: res.At, Node: res.Failure.Node, Kind: MarkFailure, Text: res.Reason})
	}
	for _, f := range res.FaultsApplied() {
		t.Faults = append(t.Faults, FaultSpan{Kind: f.Kind, Nodes: f.nodes(), Start: time.Duration(f.At), End: max(time.Duration(f.Until), time.Duration(f.At)), Text: f.Describe()})
	}
	slices.SortStableFunc(t.Marks, func(a, b Mark) int { return cmp.Compare(a.At, b.At) })
	t.Lives, t.Messages, t.Ops, t.Marks, t.Faults, t.Logs = orEmpty(t.Lives), orEmpty(t.Messages), orEmpty(t.Ops), orEmpty(t.Marks), orEmpty(t.Faults), orEmpty(t.Logs)
	return t
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (w *world) instanceNode(label string) string {
	for _, l := range w.rec.lives {
		if l.Instance == label {
			return l.Node
		}
	}
	return ""
}

func (f Fault) nodes() []string {
	switch {
	case f.Kind == FaultPartition:
		return nil
	case f.Node != "":
		return []string{f.Node}
	case f.From != "":
		return []string{f.From, f.To}
	}
	return nil
}
