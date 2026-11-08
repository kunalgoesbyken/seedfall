package engine

import (
	"slices"
	"strings"
	"testing"
)

func app(k, v string) []any { return []any{"append", k, v} }

func rd(k string, vs ...string) []any { return []any{"r", k, append([]string{}, vs...)} }

func tx(c int, mops ...[]any) op {
	in := make([][]any, len(mops))
	for i, m := range mops {
		in[i] = slices.Clone(m)
		if m[0] == "r" {
			in[i][2] = nil
		}
	}
	return op{client: c, f: "txn", in: in, out: mops, call: int64(c * 10), ret: int64(c*10 + 5), outcome: outcomeOk}
}

func TestListAppendAnomalies(t *testing.T) {
	tests := []struct {
		name string
		ops  []op
		want []string
	}{
		{"serializable", []op{tx(1, app("x", "1")), tx(2, rd("x", "1"), app("y", "2")), tx(3, rd("y", "2"), rd("x", "1"))}, nil},
		{"an unknown transaction's append may be read", []op{with(tx(1, app("x", "1")), outcomeUnknown), tx(2, rd("x", "1"))}, nil},
		{"own appends are visible", []op{tx(1, app("x", "1"), rd("x", "1"), app("x", "2"), rd("x", "1", "2"))}, nil},
		{"write cycle", []op{tx(1, app("x", "1"), app("y", "1")), tx(2, app("x", "2"), app("y", "2")), tx(3, rd("x", "1", "2"), rd("y", "2", "1"))}, []string{"G0"}},
		{"circular information flow", []op{tx(1, app("x", "1"), rd("y", "2")), tx(2, app("y", "2"), rd("x", "1"))}, []string{"G1c"}},
		{"read skew", []op{tx(1, app("x", "1"), app("y", "1")), tx(2, rd("x"), rd("y", "1")), tx(3, rd("x", "1"))}, []string{"G-single"}},
		{"write skew", []op{tx(1, rd("x"), app("y", "1")), tx(2, rd("y"), app("x", "1")), tx(3, rd("x", "1"), rd("y", "1"))}, []string{"G2"}},
		{"aborted read", []op{with(tx(1, app("x", "1")), outcomeFailed), tx(2, rd("x", "1"))}, []string{"G1a"}},
		{"intermediate read", []op{tx(1, app("x", "1"), app("x", "2")), tx(2, rd("x", "1"))}, []string{"G1b"}},
		{"internal inconsistency", []op{tx(1, app("x", "1"), rd("x"))}, []string{"internal"}},
		{"incompatible orders", []op{tx(1, app("x", "1")), tx(2, app("x", "2")), tx(3, rd("x", "1", "2")), tx(4, rd("x", "2", "1"))}, []string{"incompatible-order"}},
		{"duplicate elements", []op{tx(1, app("x", "1")), tx(2, rd("x", "1", "1"))}, []string{"duplicate-elements"}},
		{"duplicate appends", []op{tx(1, app("x", "1")), tx(2, app("x", "1"))}, []string{"duplicate-append"}},
		{"garbage read", []op{tx(1, rd("x", "9"))}, []string{"garbage-read"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := record(t, "list-append", tt.ops).check()
			var got []string
			for _, a := range res.Anomalies {
				got = append(got, a.Kind)
			}
			if !res.Checked || res.Ok != (len(tt.want) == 0) || !slices.Equal(got, tt.want) {
				t.Fatalf("ok %v anomalies %v, want %v", res.Ok, res.Anomalies, tt.want)
			}
		})
	}
}

func TestListAppendExplainsTheCycle(t *testing.T) {
	res := record(t, "list-append", []op{tx(1, rd("x"), app("y", "1")), tx(2, rd("y"), app("x", "1")), tx(3, rd("x", "1"), rd("y", "1"))}).check()
	explain := res.Anomalies[0].Explain
	for _, want := range []string{"T0 -rw x-> T1 -rw y-> T0", `T0 (client 1, ok) [r x [], append y "1"]`} {
		if !strings.Contains(explain, want) {
			t.Fatalf("explanation %q lacks %q", explain, want)
		}
	}
	f, reason := res.failure()
	if f.Class != FailIsolation || reason != "history of 3 transactions is not serializable: G2" {
		t.Fatalf("failure %v %q", f, reason)
	}
}

func TestListAppendValidatesTransactions(t *testing.T) {
	h := &history{model: "list-append"}
	for _, raw := range []string{`{"f":"put","key":"x","value":"1"}`, `{"f":"txn","value":[["delete","x",null]]}`, `{"f":"txn","value":[["append","x"]]}`} {
		if _, err := h.call(0, []byte(raw), "client#1"); err == nil {
			t.Fatalf("call(%s) accepted", raw)
		}
	}
	id, _ := h.call(0, []byte(`{"f":"txn","value":[["append","x","1"],["r","x",null]]}`), "client#1")
	for _, out := range []string{`[["append","x","1"]]`, `[["append","x","2"],["r","x",["2"]]]`} {
		if err := h.complete(5, id, []byte(out), outcomeOk); err == nil {
			t.Fatalf("output %s accepted", out)
		}
		h.ops[id].End = -1
	}
}
