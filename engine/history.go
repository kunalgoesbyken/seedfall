package engine

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/anishathalye/porcupine"
)

const (
	StatusOk   = "ok"
	StatusInfo = "info"
	StatusFail = "fail"
)

const defaultModel = "kv"

type HistoryOp struct {
	ID     int             `json:"id"`
	Client int             `json:"client"`
	F      string          `json:"f"`
	Key    string          `json:"key,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
	Status string          `json:"status,omitempty"`
	Start  int64           `json:"start"`
	End    int64           `json:"end"`
}

type checker interface {
	validate(HistoryOp) error
	check([]HistoryOp) HistoryCheck
}

type history struct {
	model  string
	ops    []HistoryOp
	owners []string
}

func (h *history) checker() checker { return checkers[h.model] }

func (h *history) call(now int64, raw []byte, owner string) (int32, error) {
	var op HistoryOp
	if err := json.Unmarshal(raw, &op); err != nil {
		return -1, fmt.Errorf("malformed operation %s: %w", raw, err)
	}
	if err := h.checker().validate(op); err != nil {
		return -1, err
	}
	op.ID, op.Start, op.End = len(h.ops), now, -1
	h.ops = append(h.ops, op)
	h.owners = append(h.owners, owner)
	return int32(op.ID), nil
}

func (h *history) open(owner string) int {
	n := 0
	for i, op := range h.ops {
		if op.End < 0 && h.owners[i] == owner {
			n++
		}
	}
	return n
}

const (
	outcomeUnknown = iota
	outcomeOk
	outcomeFailed
)

var outcomes = []string{outcomeUnknown: StatusInfo, outcomeOk: StatusOk, outcomeFailed: StatusFail}

func (h *history) complete(now int64, id int32, raw []byte, outcome int32) error {
	if id < 0 || int(id) >= len(h.ops) || h.ops[id].End >= 0 {
		return nil
	}
	op := &h.ops[id]
	op.End, op.Status = now, StatusInfo
	if int(outcome) < len(outcomes) && outcome >= 0 {
		op.Status = outcomes[outcome]
	}
	if op.Status != StatusOk {
		return nil
	}
	op.Output = slices.Clone(raw)
	return h.checker().validate(*op)
}

func (h *history) finished() []HistoryOp {
	out := slices.Clone(h.ops)
	for i := range out {
		if out[i].End < 0 {
			out[i].Status = StatusInfo
		}
	}
	return out
}

func (h *history) check() HistoryCheck {
	ops := h.finished()
	res := h.checker().check(ops)
	res.Model, res.Ops = h.model, ops
	return res
}

type Anomaly struct {
	Kind    string `json:"kind"`
	Explain string `json:"explain"`
}

type HistoryCheck struct {
	Model     string
	Checked   bool
	GaveUp    bool
	Ok        bool
	Count     int
	Anomalies []Anomaly
	Ops       []HistoryOp
	info      porcupine.LinearizationInfo
	timeline  *porcupine.Model
}

var errNoTimeline = errors.New("this model has no timeline")

func (c HistoryCheck) Visualize(w io.Writer) error {
	if c.timeline == nil {
		return errNoTimeline
	}
	return porcupine.Visualize(*c.timeline, c.info, w)
}

func (c HistoryCheck) HasTimeline() bool { return c.timeline != nil }

func (c HistoryCheck) Summary() string {
	verdict := map[bool]string{true: "ok", false: "VIOLATION"}[c.Ok]
	if !c.HasTimeline() {
		return fmt.Sprintf("%d transactions for serializability (model %s): %s", c.Count, c.Model, verdict)
	}
	return fmt.Sprintf("%d operations for linearizability (model %s): %s", c.Count, c.Model, verdict)
}

func (c HistoryCheck) failure() (Failure, string) {
	if !c.HasTimeline() {
		kinds := make([]string, len(c.Anomalies))
		for i, a := range c.Anomalies {
			kinds[i] = a.Kind
		}
		return Failure{Class: FailIsolation}, fmt.Sprintf("history of %d transactions is not serializable: %s", c.Count, joinList(kinds))
	}
	return Failure{Class: FailLinearizability}, fmt.Sprintf("history of %d operations is not linearizable under model %s", c.Count, c.Model)
}

func joinList(items []string) string { return strings.Join(items, ", ") }

func Models() []string {
	names := make([]string, 0, len(checkers))
	for name := range checkers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func knownModel(name string) error {
	if _, ok := checkers[name]; !ok {
		return fmt.Errorf("unknown model %q, want one of %s", name, joinList(Models()))
	}
	return nil
}

func CheckHistory(model string, ops []HistoryOp) (HistoryCheck, error) {
	if err := knownModel(model); err != nil {
		return HistoryCheck{}, err
	}
	h := history{model: model, ops: ops}
	for _, op := range ops {
		if err := checkers[model].validate(op); err != nil {
			return HistoryCheck{}, fmt.Errorf("operation %d: %w", op.ID, err)
		}
	}
	return h.check(), nil
}

func historyEnd(ops []HistoryOp) int64 {
	var end int64
	for _, op := range ops {
		end = max(end, op.Start, op.End)
	}
	return end
}

func PorcupineOperations(ops []HistoryOp) []porcupine.Operation {
	end := historyEnd(ops)
	out := make([]porcupine.Operation, 0, len(ops))
	for _, op := range ops {
		if op.Status == StatusFail {
			continue
		}
		ret := op.End
		if op.Status != StatusOk {
			ret = end + 1
		}
		out = append(out, porcupine.Operation{ClientId: op.Client, Input: op, Call: op.Start, Output: op, Return: ret})
	}
	return out
}

type historyEvent struct {
	Index   int             `json:"index"`
	Type    string          `json:"type"`
	Process int             `json:"process"`
	F       string          `json:"f"`
	Key     string          `json:"key,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	Time    int64           `json:"time"`
	ID      int             `json:"id"`
}

const eventInvoke = "invoke"

func WriteHistory(w io.Writer, ops []HistoryOp) error {
	var events []historyEvent
	for _, op := range ops {
		events = append(events, historyEvent{Type: eventInvoke, Process: op.Client, F: op.F, Key: op.Key, Value: op.Value, Time: op.Start, ID: op.ID})
		if op.End < 0 {
			continue
		}
		value := op.Value
		if op.Status == StatusOk {
			value = op.Output
		}
		events = append(events, historyEvent{Type: op.Status, Process: op.Client, F: op.F, Key: op.Key, Value: value, Time: op.End, ID: op.ID})
	}
	slices.SortStableFunc(events, func(a, b historyEvent) int { return cmp.Compare(a.Time, b.Time) })
	enc := json.NewEncoder(w)
	for i := range events {
		events[i].Index = i
		if err := enc.Encode(events[i]); err != nil {
			return err
		}
	}
	return nil
}

func ReadHistory(r io.Reader) ([]HistoryOp, error) {
	var ops []HistoryOp
	byID := map[int]int{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 1<<26)
	for line := 1; scanner.Scan(); line++ {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var e historyEvent
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		i, seen := byID[e.ID]
		switch {
		case e.Type == eventInvoke && seen:
			return nil, fmt.Errorf("line %d: operation %d invoked twice", line, e.ID)
		case e.Type == eventInvoke:
			byID[e.ID] = len(ops)
			ops = append(ops, HistoryOp{ID: e.ID, Client: e.Process, F: e.F, Key: e.Key, Value: e.Value, Status: StatusInfo, Start: e.Time, End: -1})
		case !seen || ops[i].End >= 0:
			return nil, fmt.Errorf("line %d: %s for operation %d without an open invoke", line, e.Type, e.ID)
		case !slices.Contains(outcomes, e.Type):
			return nil, fmt.Errorf("line %d: unknown event type %q", line, e.Type)
		default:
			ops[i].Status, ops[i].End = e.Type, e.Time
			if e.Type == StatusOk {
				ops[i].Output = e.Value
			}
		}
	}
	return ops, scanner.Err()
}
