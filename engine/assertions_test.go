package engine

import (
	"go/parser"
	"go/token"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestCatalogFindsNumericAndGroupedAssertions(t *testing.T) {
	src := `package main

import (
	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/kunalgoesbyken/seedfall/sim"
)

func f(total, acked int, msg string) {
	assert.AlwaysGreaterThanOrEqualTo(total, acked, "total covers acked", nil)
	assert.SometimesLessThan(total, 10, "total stays small", nil)
	assert.AlwaysSome(nil, "a replica answers", nil)
	sim.SometimesAll(nil, "every replica caught up")
	sim.AlwaysLessThan(total, acked, msg)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []CatalogEntry{
		{Kind: Always, Message: "total covers acked", Location: "main.go:9"},
		{Kind: Sometimes, Message: "total stays small", Location: "main.go:10"},
		{Kind: Always, Message: "a replica answers", Location: "main.go:11"},
		{Kind: Sometimes, Message: "every replica caught up", Location: "main.go:12"},
	}
	if got := fileProperties(fset, ".", file); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog %+v, want %+v", got, want)
	}
}

func antithesisWorld() (*world, *node) {
	n := &node{spec: NodeSpec{Name: "client"}}
	n.live = &instance{node: n, label: "client#1"}
	return &world{props: properties{}, trace: &tracer{}}, n
}

func TestAntithesisNumericGuidanceKeepsTheClosestMargin(t *testing.T) {
	w, n := antithesisWorld()
	lines := []string{
		`{"antithesis_assert":{"assert_type":"always","display_type":"Always","message":"total covers acked","hit":true,"must_hit":true,"condition":true,"details":{"left":9,"right":4}}}`,
		`{"antithesis_guidance":{"guidance_type":"numeric","message":"total covers acked","maximize":false,"hit":true,"guidance_data":{"left":9,"right":4}}}`,
		`{"antithesis_guidance":{"guidance_type":"numeric","message":"total covers acked","maximize":false,"hit":true,"guidance_data":{"left":6,"right":4}}}`,
		`{"antithesis_assert":{"assert_type":"sometimes","display_type":"Sometimes","message":"queue drains below 3","hit":true,"must_hit":true,"condition":false,"details":{"left":7,"right":3}}}`,
		`{"antithesis_guidance":{"guidance_type":"numeric","message":"queue drains below 3","maximize":false,"hit":true,"guidance_data":{"left":7,"right":3}}}`,
		`{"antithesis_guidance":{"guidance_type":"numeric","message":"queue drains below 3","maximize":false,"hit":true,"guidance_data":{"left":5,"right":3}}}`,
		`{"antithesis_guidance":{"guidance_type":"boolean","message":"queue drains below 3","maximize":true,"hit":true,"guidance_data":{"a":true}}}`,
		`{"antithesis_guidance":{"guidance_type":"numeric","message":"never asserted","maximize":true,"hit":true,"guidance_data":{"left":1,"right":0}}}`,
	}
	for _, l := range lines {
		w.antithesisLine(n, []byte(l))
	}
	if m := w.props["total covers acked"].Margin; m == nil || *m != 2 {
		t.Fatalf("always margin %v, want 2", m)
	}
	if m := w.props["queue drains below 3"].Margin; m == nil || *m != -2 {
		t.Fatalf("sometimes margin %v, want -2", m)
	}
	if _, ok := w.props["never asserted"]; ok {
		t.Fatal("guidance without an assertion created a property")
	}
}

func TestAntithesisSendEventBecomesATraceEvent(t *testing.T) {
	w, n := antithesisWorld()
	w.antithesisLine(n, []byte(`{"antithesis_sdk":{"sdk_version":"0.8.0"}}`))
	w.antithesisLine(n, []byte(`{"deposits done":{"acked":5}}`))
	want := []Event{{Instance: "client#1", Name: "deposits done", Details: `{"acked":5}`}}
	if !reflect.DeepEqual(w.sent, want) {
		t.Fatalf("events %+v, want %+v", w.sent, want)
	}
	if len(w.trace.lines) != 1 || !strings.Contains(w.trace.lines[0], `event deposits done from client#1: {"acked":5}`) {
		t.Fatalf("trace %q", w.trace.lines)
	}
}

func TestNumericMarginsAggregateAcrossSeeds(t *testing.T) {
	w, n := antithesisWorld()
	w.observeNumeric(n.live, Always, true, 5, "balance covers debits", "")
	w.observeNumeric(n.live, Always, true, 1, "balance covers debits", "")
	w.observeNumeric(n.live, Sometimes, false, -4, "backlog exceeds 100", "")
	w.observeNumeric(n.live, Sometimes, false, -9, "backlog exceeds 100", "")
	c := newCoverage(nil)
	c.add(w.props)
	two, minusOne := 2.0, -1.0
	c.add(properties{"balance covers debits": {Kind: Always, Hits: 1, True: 1, Margin: &two}, "backlog exceeds 100": {Kind: Sometimes, Hits: 1, Margin: &minusOne}})
	if m := c["balance covers debits"].Margin; *m != 1 || c["balance covers debits"].Kind.MarginNote(m) != "smallest margin 1" {
		t.Fatalf("always margin %v", *m)
	}
	if m := c["backlog exceeds 100"].Margin; *m != -1 || c["backlog exceeds 100"].Kind.MarginNote(m) != "largest margin -1" {
		t.Fatalf("sometimes margin %v", *m)
	}
	if c["backlog exceeds 100"].SeedsReached != 2 || c["backlog exceeds 100"].SeedsTrue != 0 {
		t.Fatalf("coverage %+v", *c["backlog exceeds 100"])
	}
}

func TestNonFiniteMarginsAreNotKept(t *testing.T) {
	w, n := antithesisWorld()
	w.observeNumeric(n.live, Always, true, math.NaN(), "ratio stays positive", "")
	w.observeNumeric(n.live, Always, true, math.Inf(1), "ratio stays positive", "")
	if p := w.props["ratio stays positive"]; p == nil || p.Hits != 2 || p.Margin != nil {
		t.Fatalf("property %+v", p)
	}
	w.observeNumeric(n.live, Always, true, 3, "ratio stays positive", "")
	if m := w.props["ratio stays positive"].Margin; m == nil || *m != 3 {
		t.Fatalf("margin %v", m)
	}
}
