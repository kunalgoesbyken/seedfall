package engine

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/anishathalye/porcupine"
)

type op struct {
	client    int
	f, key    string
	in, out   any
	call, ret int64
	outcome   int32
	pending   bool
}

func record(t *testing.T, model string, ops []op) *history {
	t.Helper()
	h := &history{model: model}
	ids := make([]int32, len(ops))
	for i, o := range ops {
		raw, _ := json.Marshal(map[string]any{"client": o.client, "f": o.f, "key": o.key, "value": o.in})
		id, err := h.call(o.call, raw, "client#1")
		if err != nil {
			t.Fatalf("op %d rejected: %v", i, err)
		}
		ids[i] = id
	}
	for i, o := range ops {
		if o.pending {
			continue
		}
		out, _ := json.Marshal(o.out)
		if err := h.complete(o.ret, ids[i], out, o.outcome); err != nil {
			t.Fatalf("op %d output rejected: %v", i, err)
		}
	}
	return h
}

func ok(c int, f, k string, in, out any, call, ret int64) op {
	return op{client: c, f: f, key: k, in: in, out: out, call: call, ret: ret, outcome: outcomeOk}
}

func put(c int, k, v string, call, ret int64) op { return ok(c, "put", k, v, "", call, ret) }

func get(c int, k, v string, call, ret int64) op { return ok(c, "get", k, nil, v, call, ret) }

func with(o op, outcome int32) op {
	o.outcome = outcome
	return o
}

func pending(o op) op {
	o.pending = true
	return o
}

type verdict struct {
	name string
	ops  []op
	ok   bool
}

func checkVerdicts(t *testing.T, model string, tests []verdict) {
	t.Helper()
	for _, tt := range tests {
		t.Run(model+"/"+tt.name, func(t *testing.T) {
			got := record(t, model, tt.ops).check()
			if !got.Checked || got.Ok != tt.ok {
				t.Fatalf("checked %v ok %v, want ok %v", got.Checked, got.Ok, tt.ok)
			}
		})
	}
}

func TestLinearizabilityVerdicts(t *testing.T) {
	checkVerdicts(t, "kv", []verdict{
		{"read overlapping a write sees it", []op{put(1, "x", "1", 0, 10), get(2, "x", "1", 5, 15)}, true},
		{"read overlapping a write misses it", []op{put(1, "x", "1", 0, 10), get(2, "x", "", 5, 15)}, true},
		{"read after an acknowledged write misses it", []op{put(1, "x", "1", 0, 10), get(2, "x", "", 20, 30)}, false},
		{"stale read after an overwrite", []op{put(1, "x", "1", 0, 10), put(1, "x", "2", 20, 30), get(2, "x", "1", 40, 50)}, false},
		{"keys are checked independently", []op{put(1, "x", "1", 0, 10), get(2, "y", "", 20, 30)}, true},
		{"an unknown write may have happened", []op{with(put(1, "x", "1", 0, 10), outcomeUnknown), get(2, "x", "1", 20, 30)}, true},
		{"an unknown write may not have happened", []op{with(put(1, "x", "1", 0, 10), outcomeUnknown), get(2, "x", "", 20, 30)}, true},
		{"a write that never returned may land later", []op{pending(put(1, "x", "1", 0, 0)), get(2, "x", "", 20, 30), get(2, "x", "1", 40, 50)}, true},
		{"a failed write must not be seen", []op{with(put(1, "x", "1", 0, 10), outcomeFailed), get(2, "x", "1", 20, 30)}, false},
		{"an unknown read is ignored", []op{put(1, "x", "1", 0, 10), with(get(2, "x", "bogus", 20, 30), outcomeUnknown)}, true},
	})
}

func TestModelVerdicts(t *testing.T) {
	cas := func(c int, old, new string, swapped bool, call, ret int64) op {
		return ok(c, "cas", "l", []string{old, new}, swapped, call, ret)
	}
	checkVerdicts(t, "cas", []verdict{
		{"one of two racing swaps wins", []op{cas(1, "", "a", true, 0, 10), cas(2, "", "b", false, 5, 15), get(3, "l", "a", 20, 30)}, true},
		{"two racing swaps from the same value both win", []op{cas(1, "", "a", true, 0, 10), cas(2, "", "b", true, 5, 15)}, false},
		{"a swap fails although the value matched", []op{put(1, "l", "a", 0, 10), cas(2, "a", "b", false, 20, 30)}, false},
		{"a failed swap is dropped", []op{with(cas(1, "x", "y", true, 0, 10), outcomeFailed), get(2, "l", "", 20, 30)}, true},
	})
	appendOp := func(c int, v string, call, ret int64) op { return ok(c, "append", "log", v, "", call, ret) }
	readLog := func(c int, l []string, call, ret int64) op { return ok(c, "read", "log", nil, l, call, ret) }
	checkVerdicts(t, "append", []verdict{
		{"reads see appends in order", []op{appendOp(1, "a", 0, 10), appendOp(2, "b", 20, 30), readLog(3, []string{"a", "b"}, 40, 50)}, true},
		{"concurrent appends land in either order", []op{appendOp(1, "a", 0, 10), appendOp(2, "b", 5, 15), readLog(3, []string{"b", "a"}, 40, 50)}, true},
		{"an acknowledged append is lost", []op{appendOp(1, "a", 0, 10), appendOp(2, "b", 20, 30), readLog(3, []string{"b"}, 40, 50)}, false},
		{"appends are reordered after they returned", []op{appendOp(1, "a", 0, 10), appendOp(2, "b", 20, 30), readLog(3, []string{"b", "a"}, 40, 50)}, false},
	})
	enq := func(c int, v string, call, ret int64) op { return ok(c, "enqueue", "q", v, "", call, ret) }
	deq := func(c int, v string, call, ret int64) op { return ok(c, "dequeue", "q", nil, v, call, ret) }
	checkVerdicts(t, "queue", []verdict{
		{"first in, first out", []op{enq(1, "a", 0, 10), enq(1, "b", 20, 30), deq(2, "a", 40, 50), deq(2, "b", 60, 70), deq(2, "", 80, 90)}, true},
		{"out of order", []op{enq(1, "a", 0, 10), enq(1, "b", 20, 30), deq(2, "b", 40, 50)}, false},
		{"delivered twice", []op{enq(1, "a", 0, 10), deq(2, "a", 20, 30), deq(3, "a", 40, 50)}, false},
		{"an unknown dequeue may have taken the item", []op{enq(1, "a", 0, 10), with(deq(2, "", 20, 30), outcomeUnknown), deq(3, "", 40, 50)}, true},
		{"empty while an item is queued", []op{enq(1, "a", 0, 10), deq(2, "", 20, 30)}, false},
	})
	add := func(c int, v string, call, ret int64) op { return ok(c, "add", "s", v, "", call, ret) }
	readSet := func(c int, l []string, call, ret int64) op { return ok(c, "read", "s", nil, l, call, ret) }
	checkVerdicts(t, "set", []verdict{
		{"read order does not matter", []op{add(1, "a", 0, 10), add(2, "b", 20, 30), readSet(3, []string{"b", "a"}, 40, 50)}, true},
		{"adding twice keeps one", []op{add(1, "a", 0, 10), add(2, "a", 20, 30), readSet(3, []string{"a"}, 40, 50)}, true},
		{"an acknowledged add is lost", []op{add(1, "a", 0, 10), add(2, "b", 20, 30), readSet(3, []string{"b"}, 40, 50)}, false},
		{"a read repeats an element", []op{add(1, "a", 0, 10), readSet(3, []string{"a", "a"}, 40, 50)}, false},
	})
	incr := func(c int, n int, call, ret int64) op { return ok(c, "add", "n", n, "", call, ret) }
	readN := func(c int, n int, call, ret int64) op { return ok(c, "read", "n", nil, n, call, ret) }
	checkVerdicts(t, "counter", []verdict{
		{"reads see every acknowledged add", []op{incr(1, 2, 0, 10), incr(2, 3, 20, 30), readN(3, 5, 40, 50)}, true},
		{"an unknown add may count", []op{incr(1, 2, 0, 10), with(incr(2, 3, 20, 30), outcomeUnknown), readN(3, 5, 40, 50)}, true},
		{"an unknown add may not count", []op{incr(1, 2, 0, 10), with(incr(2, 3, 20, 30), outcomeUnknown), readN(3, 2, 40, 50)}, true},
		{"a lost update", []op{incr(1, 2, 0, 10), incr(2, 3, 20, 30), readN(3, 3, 40, 50)}, false},
	})
}

func TestHistoryRejectsMalformedCalls(t *testing.T) {
	h := &history{model: defaultModel}
	for _, raw := range []string{"not json", `{"f":"delete","key":"x"}`, `{"f":"put","key":"x","value":7}`} {
		if id, err := h.call(0, []byte(raw), "client#1"); id != -1 || err == nil {
			t.Fatalf("call(%q) = %d, %v, want -1 and an error", raw, id, err)
		}
	}
	q := &history{model: "queue"}
	if _, err := q.call(0, []byte(`{"f":"put","key":"x","value":"1"}`), "client#1"); err == nil || !strings.Contains(err.Error(), "dequeue, enqueue") {
		t.Fatalf("a put under the queue model gave %v, want the queue's operations", err)
	}
	id, _ := q.call(0, []byte(`{"f":"dequeue","key":"x"}`), "client#1")
	if err := q.complete(5, id, []byte(`["a"]`), outcomeOk); err == nil {
		t.Fatal("a dequeue that returned a list was accepted")
	}
	id, _ = q.call(0, []byte(`{"f":"dequeue","key":"x"}`), "client#1")
	if err := q.complete(5, id, nil, outcomeOk); err == nil {
		t.Fatal("a dequeue whose output could not be encoded was accepted")
	}
	if _, err := q.call(0, []byte(`{"f":"enqueue","key":"x"}`), "client#1"); err == nil {
		t.Fatal("an enqueue without a value was accepted")
	}
	id, _ = h.call(0, []byte(`{"client":1,"f":"put","key":"x","value":"1"}`), "client#1")
	h.complete(5, id, nil, outcomeOk)
	h.complete(9, id, nil, outcomeFailed)
	h.complete(9, id+1, nil, outcomeOk)
	h.complete(9, -1, nil, outcomeOk)
	if len(h.ops) != 1 || h.ops[0].End != 5 || h.ops[0].Status != StatusOk {
		t.Fatalf("ops = %+v, want one put returned at 5", h.ops)
	}
	if got := (&history{model: defaultModel}).check(); got.Checked {
		t.Fatal("an empty history must not count as checked")
	}
}

func TestHistoryFileRoundTrip(t *testing.T) {
	h := record(t, "kv", []op{put(1, "x", "1", 0, 10), with(put(2, "x", "2", 5, 12), outcomeUnknown), with(put(3, "y", "1", 6, 8), outcomeFailed), pending(put(4, "y", "2", 7, 0)), get(5, "x", "2", 20, 30)})
	var buf bytes.Buffer
	if err := WriteHistory(&buf, h.finished()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 9 || !strings.Contains(lines[0], `"type":"invoke"`) || !strings.Contains(lines[8], `"type":"ok"`) {
		t.Fatalf("history file:\n%s", buf.String())
	}
	back, err := ReadHistory(&buf)
	if err != nil {
		t.Fatal(err)
	}
	want := h.finished()
	for i := range want {
		if want[i].Status == StatusOk && want[i].F == "put" {
			want[i].Output = json.RawMessage(`""`)
		}
	}
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("read back %+v\nwant %+v", back, want)
	}
	res, err := CheckHistory("kv", back)
	if err != nil || !res.Checked || !res.Ok || res.Count != 4 {
		t.Fatalf("recheck: %+v %v", res, err)
	}
	if _, err := CheckHistory("queue", back); err == nil {
		t.Fatal("a kv history checked under the queue model")
	}
	if _, err := CheckHistory("nope", back); err == nil {
		t.Fatal("an unknown model was accepted")
	}
	for _, bad := range []string{`{"type":"ok","id":3}`, `{"type":"invoke","id":1}` + "\n" + `{"type":"invoke","id":1}`, `{"type":"invoke","id":1}` + "\n" + `{"type":"done","id":1}`} {
		if _, err := ReadHistory(strings.NewReader(bad)); err == nil {
			t.Fatalf("ReadHistory(%q) accepted", bad)
		}
	}
}

func TestCustomPorcupineModel(t *testing.T) {
	acquire := func(c int, call, ret int64) op { return ok(c, "put", "lock", "acquire", "", call, ret) }
	release := func(c int, call, ret int64) op { return ok(c, "put", "lock", "release", "", call, ret) }
	mutex := porcupine.Model{
		Init: func() any { return false },
		Step: func(state, input, _ any) (bool, any) {
			var f string
			json.Unmarshal(input.(HistoryOp).Value, &f)
			if f == "acquire" {
				return !state.(bool), true
			}
			return state.(bool), false
		},
	}
	for _, tt := range []verdict{
		{"held in turn", []op{acquire(1, 0, 10), release(1, 20, 30), acquire(2, 40, 50)}, true},
		{"held twice", []op{acquire(1, 0, 10), acquire(2, 20, 30)}, false},
	} {
		got := porcupine.CheckOperations(mutex, PorcupineOperations(record(t, "kv", tt.ops).finished()))
		if got != tt.ok {
			t.Fatalf("%s: got %v, want %v", tt.name, got, tt.ok)
		}
	}
}

func TestEveryModelDescribesItsTimeline(t *testing.T) {
	h := record(t, "queue", []op{ok(1, "enqueue", "q", "a", "", 0, 10), ok(2, "dequeue", "q", nil, "b", 20, 30)})
	res := h.check()
	var buf bytes.Buffer
	if res.Ok || res.Visualize(&buf) != nil || !strings.Contains(buf.String(), "dequeue(q)") {
		t.Fatalf("ok %v, timeline lacks the dequeue", res.Ok)
	}
	if (HistoryCheck{}).Visualize(&buf) == nil {
		t.Fatal("a transactional check produced a timeline")
	}
}
