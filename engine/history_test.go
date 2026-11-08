package engine

import (
	"encoding/json"
	"testing"
)

type op struct {
	client     int
	kind       string
	key, value string
	call, ret  int64
	outcome    int32
	pending    bool
}

func record(t *testing.T, ops []op) *history {
	t.Helper()
	h := &history{}
	ids := make([]int32, len(ops))
	for i, o := range ops {
		raw, _ := json.Marshal(kvInput{Client: o.client, Kind: o.kind, Key: o.key, Value: o.value})
		if ids[i] = h.call(o.call, raw); ids[i] < 0 {
			t.Fatalf("op %d rejected", i)
		}
	}
	for i, o := range ops {
		if !o.pending {
			h.complete(o.ret, ids[i], o.value, o.outcome)
		}
	}
	return h
}

func put(c int, k, v string, call, ret int64) op {
	return op{client: c, kind: "put", key: k, value: v, call: call, ret: ret, outcome: outcomeOk}
}

func get(c int, k, v string, call, ret int64) op {
	return op{client: c, kind: "get", key: k, value: v, call: call, ret: ret, outcome: outcomeOk}
}

func with(o op, outcome int32) op {
	o.outcome = outcome
	return o
}

func pending(o op) op {
	o.pending = true
	return o
}

func TestLinearizabilityVerdicts(t *testing.T) {
	tests := []struct {
		name string
		ops  []op
		ok   bool
	}{
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkHistory(record(t, tt.ops), 100)
			if !got.Checked || got.Ok != tt.ok {
				t.Fatalf("checked %v ok %v, want ok %v", got.Checked, got.Ok, tt.ok)
			}
		})
	}
}

func TestHistoryIgnoresMalformedCalls(t *testing.T) {
	h := &history{}
	for _, raw := range []string{"not json", `{"kind":"delete","key":"x"}`} {
		if id := h.call(0, []byte(raw)); id != -1 {
			t.Fatalf("call(%q) = %d, want -1", raw, id)
		}
	}
	id := h.call(0, []byte(`{"client":1,"kind":"put","key":"x","value":"1"}`))
	h.complete(5, id, "", outcomeOk)
	h.complete(9, id, "", outcomeFailed)
	h.complete(9, id+1, "", outcomeOk)
	h.complete(9, -1, "", outcomeOk)
	if len(h.ops) != 1 || h.ops[0].Return != 5 || h.ops[0].Output.(kvOutput).Failed {
		t.Fatalf("ops = %+v, want one put returned at 5", h.ops)
	}
	if got := checkHistory(&history{}, 10); got.Checked {
		t.Fatal("an empty history must not count as checked")
	}
}
