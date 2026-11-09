package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/anishathalye/porcupine"
)

type kind int

const (
	none kind = iota
	text
	pair
	list
	set
	number
	flag
)

type value struct {
	S    string
	Pair [2]string
	L    []string
	N    int64
	B    bool
}

func decode(k kind, raw json.RawMessage) (value, error) {
	var v value
	if k == none {
		return v, nil
	}
	if len(raw) == 0 {
		return v, errMissingValue
	}
	targets := map[kind]any{text: &v.S, pair: &v.Pair, list: &v.L, set: &v.L, number: &v.N, flag: &v.B}
	if err := json.Unmarshal(raw, targets[k]); err != nil {
		return v, err
	}
	if k == set {
		slices.Sort(v.L)
	}
	return v, nil
}

func (v value) equal(o value) bool {
	return v.S == o.S && v.N == o.N && v.B == o.B && slices.Equal(v.L, o.L)
}

func show(k kind, v value) string {
	switch k {
	case text:
		return fmt.Sprintf("%q", v.S)
	case pair:
		return fmt.Sprintf("%q, %q", v.Pair[0], v.Pair[1])
	case list, set:
		return fmt.Sprintf("%q", v.L)
	case number:
		return fmt.Sprint(v.N)
	case flag:
		return fmt.Sprint(v.B)
	}
	return "ok"
}

var errMissingValue = errors.New("value is missing or could not be encoded as JSON")

type operation struct {
	in, out kind
	read    bool
}

type model struct {
	ops   map[string]operation
	init  any
	apply func(state any, f string, arg value) (any, value)
}

type linInput struct {
	F, Key string
	Arg    value
}

type linOutput struct {
	Value   value
	Unknown bool
}

var checkers = map[string]checker{
	"kv": model{ops: map[string]operation{"put": {in: text}, "get": {out: text, read: true}}, init: "", apply: register},
	"cas": model{
		ops:  map[string]operation{"put": {in: text}, "get": {out: text, read: true}, "cas": {in: pair, out: flag}},
		init: "", apply: register,
	},
	"append":      model{ops: map[string]operation{"append": {in: text}, "read": {out: list, read: true}}, init: []string(nil), apply: appendLog},
	"queue":       model{ops: map[string]operation{"enqueue": {in: text}, "dequeue": {out: text}}, init: []string(nil), apply: fifo},
	"set":         model{ops: map[string]operation{"add": {in: text}, "read": {out: set, read: true}}, init: []string(nil), apply: grow},
	"counter":     model{ops: map[string]operation{"add": {in: number}, "read": {out: number, read: true}}, init: int64(0), apply: count},
	"list-append": listAppend{},
}

func register(state any, f string, arg value) (any, value) {
	s := state.(string)
	switch f {
	case "put":
		return arg.S, value{}
	case "cas":
		if s == arg.Pair[0] {
			return arg.Pair[1], value{B: true}
		}
		return s, value{}
	}
	return s, value{S: s}
}

func appendLog(state any, f string, arg value) (any, value) {
	l := state.([]string)
	if f == "append" {
		return append(slices.Clip(l), arg.S), value{}
	}
	return l, value{L: l}
}

func fifo(state any, f string, arg value) (any, value) {
	l := state.([]string)
	if f == "enqueue" {
		return append(slices.Clip(l), arg.S), value{}
	}
	if len(l) == 0 {
		return l, value{}
	}
	return l[1:], value{S: l[0]}
}

func grow(state any, f string, arg value) (any, value) {
	l := state.([]string)
	if f == "read" {
		return l, value{L: l}
	}
	i, found := slices.BinarySearch(l, arg.S)
	if found {
		return l, value{}
	}
	return slices.Insert(slices.Clip(l), i, arg.S), value{}
}

func count(state any, f string, arg value) (any, value) {
	n := state.(int64)
	if f == "add" {
		return n + arg.N, value{}
	}
	return n, value{N: n}
}

func (m model) names() string {
	names := make([]string, 0, len(m.ops))
	for f := range m.ops {
		names = append(names, f)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func (m model) validate(op HistoryOp) error {
	o, ok := m.ops[op.F]
	if !ok {
		return fmt.Errorf("operation %q is not one of %s", op.F, m.names())
	}
	if _, err := decode(o.in, op.Value); err != nil {
		return fmt.Errorf("%s(%s) input %s: %w", op.F, op.Key, op.Value, err)
	}
	if op.Status != StatusOk {
		return nil
	}
	if _, err := decode(o.out, op.Output); err != nil {
		return fmt.Errorf("%s(%s) output %s: %w", op.F, op.Key, op.Output, err)
	}
	return nil
}

func (m model) operations(ops []HistoryOp) []porcupine.Operation {
	end := historyEnd(ops)
	out := make([]porcupine.Operation, 0, len(ops))
	for _, op := range ops {
		o := m.ops[op.F]
		unknown := op.Status != StatusOk
		if op.Status == StatusFail || (unknown && o.read) {
			continue
		}
		arg, _ := decode(o.in, op.Value)
		p := porcupine.Operation{ClientId: op.Client, Input: linInput{F: op.F, Key: op.Key, Arg: arg}, Call: op.Start, Output: linOutput{Unknown: true}, Return: end + 1}
		if !unknown {
			res, _ := decode(o.out, op.Output)
			p.Output, p.Return = linOutput{Value: res}, op.End
		}
		out = append(out, p)
	}
	return out
}

const (
	historyCheckBudget = 30 * time.Second
	timelineBudget     = 10 * time.Second
)

func (m model) check(ops []HistoryOp) HistoryCheck {
	p := m.porcupine()
	res := HistoryCheck{timeline: &p}
	checked := m.operations(ops)
	if len(checked) == 0 {
		return res
	}
	res.Count = len(checked)
	result := porcupine.CheckOperationsTimeout(p, checked, historyCheckBudget)
	if result == porcupine.Unknown {
		res.GaveUp = true
		return res
	}
	res.Checked, res.Ok = true, result == porcupine.Ok
	if !res.Ok {
		_, res.info = porcupine.CheckOperationsVerbose(p, checked, timelineBudget)
	}
	return res
}

func byKey(ops []porcupine.Operation) [][]porcupine.Operation {
	parts := map[string][]porcupine.Operation{}
	var order []string
	for _, op := range ops {
		k := op.Input.(linInput).Key
		if _, seen := parts[k]; !seen {
			order = append(order, k)
		}
		parts[k] = append(parts[k], op)
	}
	out := make([][]porcupine.Operation, 0, len(order))
	for _, k := range order {
		out = append(out, parts[k])
	}
	return out
}

func sameState(a, b any) bool {
	if l, ok := a.([]string); ok {
		return slices.Equal(l, b.([]string))
	}
	return a == b
}

func (m model) porcupine() porcupine.Model {
	return porcupine.Model{
		Partition: byKey,
		Init:      func() any { return m.init },
		Step: func(state, input, output any) (bool, any) {
			in, out := input.(linInput), output.(linOutput)
			next, got := m.apply(state, in.F, in.Arg)
			return out.Unknown || got.equal(out.Value), next
		},
		Equal: sameState,
		DescribeOperation: func(input, output any) string {
			in, out := input.(linInput), output.(linOutput)
			o := m.ops[in.F]
			call := in.Key
			if o.in != none {
				call += ", " + show(o.in, in.Arg)
			}
			result := show(o.out, out.Value)
			if out.Unknown {
				result = "?"
			}
			return fmt.Sprintf("%s(%s) -> %s", in.F, call, result)
		},
		DescribeState: func(state any) string {
			if n, ok := state.(int64); ok {
				return fmt.Sprint(n)
			}
			return fmt.Sprintf("%q", state)
		},
	}
}
