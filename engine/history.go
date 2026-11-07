package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/anishathalye/porcupine"
)

type kvInput struct {
	Client int    `json:"client"`
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

type kvOutput struct {
	Value   string
	Unknown bool
	Failed  bool
}

type history struct {
	ops []porcupine.Operation
}

func (h *history) call(now int64, raw []byte) int32 {
	var in kvInput
	if err := json.Unmarshal(raw, &in); err != nil || (in.Kind != "put" && in.Kind != "get") {
		return -1
	}
	h.ops = append(h.ops, porcupine.Operation{ClientId: in.Client, Input: in, Call: now, Return: -1})
	return int32(len(h.ops) - 1)
}

const (
	outcomeUnknown = iota
	outcomeOk
	outcomeFailed
)

func (h *history) complete(now int64, id int32, value string, outcome int32) {
	if id < 0 || int(id) >= len(h.ops) || h.ops[id].Return >= 0 {
		return
	}
	h.ops[id].Output = kvOutput{Value: value, Unknown: outcome == outcomeUnknown, Failed: outcome == outcomeFailed}
	h.ops[id].Return = now
}

func (h *history) operations(end int64) []porcupine.Operation {
	out := make([]porcupine.Operation, 0, len(h.ops))
	for _, op := range h.ops {
		pending := op.Return < 0
		if !pending && op.Output.(kvOutput).Failed {
			continue
		}
		unknown := pending || op.Output.(kvOutput).Unknown
		if unknown && op.Input.(kvInput).Kind == "get" {
			continue
		}
		if unknown {
			op.Output, op.Return = kvOutput{Unknown: true}, end+1
		}
		out = append(out, op)
	}
	return out
}

var kvModel = porcupine.Model{
	Partition: func(ops []porcupine.Operation) [][]porcupine.Operation {
		byKey := map[string][]porcupine.Operation{}
		var order []string
		for _, op := range ops {
			k := op.Input.(kvInput).Key
			if _, seen := byKey[k]; !seen {
				order = append(order, k)
			}
			byKey[k] = append(byKey[k], op)
		}
		parts := make([][]porcupine.Operation, 0, len(order))
		for _, k := range order {
			parts = append(parts, byKey[k])
		}
		return parts
	},
	Init: func() any { return "" },
	Step: func(state, input, output any) (bool, any) {
		in, out := input.(kvInput), output.(kvOutput)
		if in.Kind == "put" {
			return true, in.Value
		}
		return out.Unknown || out.Value == state.(string), state
	},
	Equal: func(a, b any) bool { return a == b },
	DescribeOperation: func(input, output any) string {
		in, out := input.(kvInput), output.(kvOutput)
		result := out.Value
		if out.Unknown {
			result = "?"
		}
		if in.Kind == "put" {
			return fmt.Sprintf("put(%s, %s) -> %s", in.Key, in.Value, map[bool]string{true: "?", false: "ok"}[out.Unknown])
		}
		return fmt.Sprintf("get(%s) -> %q", in.Key, result)
	},
	DescribeState: func(state any) string { return fmt.Sprintf("%q", state) },
}

type Linearizability struct {
	Checked bool
	Ok      bool
	Ops     int
	info    porcupine.LinearizationInfo
}

func (l Linearizability) Visualize(w io.Writer) error { return porcupine.Visualize(kvModel, l.info, w) }

func checkHistory(h *history, end int64) Linearizability {
	ops := h.operations(end)
	if len(ops) == 0 {
		return Linearizability{}
	}
	result, info := porcupine.CheckOperationsVerbose(kvModel, ops, time.Minute)
	return Linearizability{Checked: true, Ok: result != porcupine.Illegal, Ops: len(ops), info: info}
}
