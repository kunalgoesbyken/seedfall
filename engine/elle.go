package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	mopAppend = "append"
	mopRead   = "r"
	txnOp     = "txn"
)

type mop struct {
	F, Key string
	Value  string
	Read   []string
}

func (m *mop) UnmarshalJSON(b []byte) error {
	var parts []json.RawMessage
	if err := json.Unmarshal(b, &parts); err != nil {
		return err
	}
	if len(parts) != 3 {
		return fmt.Errorf("micro-op %s is not [f, key, value]", b)
	}
	if err := json.Unmarshal(parts[0], &m.F); err != nil {
		return err
	}
	if err := json.Unmarshal(parts[1], &m.Key); err != nil {
		return err
	}
	switch m.F {
	case mopAppend:
		return json.Unmarshal(parts[2], &m.Value)
	case mopRead:
		return json.Unmarshal(parts[2], &m.Read)
	}
	return fmt.Errorf("micro-op %q is not %s or %s", m.F, mopAppend, mopRead)
}

func (m mop) String() string {
	if m.F == mopAppend {
		return fmt.Sprintf("%s %s %q", m.F, m.Key, m.Value)
	}
	return fmt.Sprintf("%s %s %q", m.F, m.Key, m.Read)
}

type listAppend struct{}

func (listAppend) validate(op HistoryOp) error {
	if op.F != txnOp {
		return fmt.Errorf("operation %q is not %s, record transactions with sim.Txn", op.F, txnOp)
	}
	var in, out []mop
	if err := json.Unmarshal(op.Value, &in); err != nil {
		return fmt.Errorf("txn input %s: %w", op.Value, err)
	}
	if op.Status != StatusOk {
		return nil
	}
	if err := json.Unmarshal(op.Output, &out); err != nil {
		return fmt.Errorf("txn output %s: %w", op.Output, err)
	}
	if len(in) != len(out) {
		return fmt.Errorf("txn output has %d micro-ops, input has %d", len(out), len(in))
	}
	for i := range in {
		if in[i].F != out[i].F || in[i].Key != out[i].Key || in[i].Value != out[i].Value {
			return fmt.Errorf("txn output micro-op %d is %s, input is %s", i, out[i], in[i])
		}
	}
	return nil
}

type txn struct {
	op   HistoryOp
	mops []mop
}

func (t txn) label() string { return fmt.Sprintf("T%d", t.op.ID) }

func (t txn) String() string {
	parts := make([]string, len(t.mops))
	for i, m := range t.mops {
		parts[i] = m.String()
	}
	return fmt.Sprintf("%s (client %d, %s) [%s]", t.label(), t.op.Client, t.op.Status, strings.Join(parts, ", "))
}

func parseTxns(ops []HistoryOp) []txn {
	txns := make([]txn, 0, len(ops))
	for _, op := range ops {
		raw := op.Value
		if op.Status == StatusOk {
			raw = op.Output
		}
		var mops []mop
		json.Unmarshal(raw, &mops)
		txns = append(txns, txn{op: op, mops: mops})
	}
	return txns
}

type element struct{ key, value string }

type writer struct {
	txn  int
	last bool
}

type read struct {
	txn  int
	key  string
	list []string
}

const (
	edgeWW = "ww"
	edgeWR = "wr"
	edgeRW = "rw"
)

var anomalyOrder = []string{"G0", "G1a", "G1b", "G1c", "G-single", "G2", "internal", "incompatible-order", "duplicate-elements", "duplicate-append", "garbage-read"}

type elle struct {
	txns    []txn
	writers map[element]writer
	reads   []read
	g       *graph
	found   map[string]string
}

func (e *elle) report(kind, format string, args ...any) {
	if _, ok := e.found[kind]; !ok {
		e.found[kind] = fmt.Sprintf(format, args...)
	}
}

func (listAppend) check(ops []HistoryOp) HistoryCheck {
	e := &elle{txns: parseTxns(ops), writers: map[element]writer{}, found: map[string]string{}}
	if len(e.txns) == 0 {
		return HistoryCheck{}
	}
	e.g = newGraph(len(e.txns))
	e.indexWrites()
	e.indexReads()
	e.inferEdges()
	e.findCycles()
	res := HistoryCheck{Checked: true, Count: len(e.txns)}
	for _, kind := range anomalyOrder {
		if explain, ok := e.found[kind]; ok {
			res.Anomalies = append(res.Anomalies, Anomaly{Kind: kind, Explain: explain})
		}
	}
	res.Ok = len(res.Anomalies) == 0
	return res
}

func (e *elle) indexWrites() {
	for i, t := range e.txns {
		total := map[string]int{}
		for _, m := range t.mops {
			if m.F == mopAppend {
				total[m.Key]++
			}
		}
		seen := map[string]int{}
		for _, m := range t.mops {
			if m.F != mopAppend {
				continue
			}
			seen[m.Key]++
			el := element{m.Key, m.Value}
			if w, dup := e.writers[el]; dup {
				e.report("duplicate-append", "%q was appended to %s by both %s and %s; appended values must be unique per key", m.Value, m.Key, e.txns[w.txn].label(), t.label())
				continue
			}
			e.writers[el] = writer{txn: i, last: seen[m.Key] == total[m.Key]}
		}
	}
}

func (e *elle) indexReads() {
	for i, t := range e.txns {
		if t.op.Status != StatusOk {
			continue
		}
		known := map[string][]string{}
		mine := map[string][]string{}
		for _, m := range t.mops {
			prior, hasPrior := known[m.Key]
			switch {
			case m.F == mopAppend && hasPrior:
				known[m.Key] = append(slices.Clip(prior), m.Value)
			case m.F == mopAppend:
				mine[m.Key] = append(mine[m.Key], m.Value)
			case hasPrior && !slices.Equal(m.Read, prior):
				e.report("internal", "%s read %s as %q after it had seen %q", t, m.Key, m.Read, prior)
			case !hasPrior && !hasSuffix(m.Read, mine[m.Key]):
				e.report("internal", "%s read %s as %q, which does not end with its own appends %q", t, m.Key, m.Read, mine[m.Key])
			}
			if m.F == mopRead {
				known[m.Key] = m.Read
				e.reads = append(e.reads, read{txn: i, key: m.Key, list: m.Read})
			}
		}
	}
}

func hasSuffix(l, suffix []string) bool {
	return len(l) >= len(suffix) && slices.Equal(l[len(l)-len(suffix):], suffix)
}

func (e *elle) inferEdges() {
	byKey := map[string][]read{}
	for _, r := range e.reads {
		byKey[r.key] = append(byKey[r.key], r)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		e.inferKey(k, byKey[k])
	}
}

func (e *elle) inferKey(k string, reads []read) {
	order := slices.MaxFunc(reads, func(a, b read) int { return len(a.list) - len(b.list) })
	for i := 1; i < len(order.list); i++ {
		a, okA := e.writers[element{k, order.list[i-1]}]
		b, okB := e.writers[element{k, order.list[i]}]
		if okA && okB {
			e.edge(a.txn, b.txn, edgeWW, k)
		}
	}
	for _, r := range reads {
		t := e.txns[r.txn]
		if len(slices.Compact(sortedCopy(r.list))) != len(r.list) {
			e.report("duplicate-elements", "%s read %s as %q, which repeats an element", t, k, r.list)
		}
		if !hasPrefix(order.list, r.list) {
			e.report("incompatible-order", "%s was read as %q by %s and as %q by %s, and neither is a prefix of the other", k, r.list, t.label(), order.list, e.txns[order.txn].label())
			continue
		}
		for _, v := range r.list {
			w, ok := e.writers[element{k, v}]
			switch {
			case !ok:
				e.report("garbage-read", "%s read %q in %s, which no transaction appended", t, v, k)
			case w.txn != r.txn && e.txns[w.txn].op.Status == StatusFail:
				e.report("G1a", "%s read %q in %s, appended by %s, which failed", t, v, k, e.txns[w.txn])
			}
		}
		if n := len(r.list); n > 0 {
			if w, ok := e.writers[element{k, r.list[n-1]}]; ok {
				if w.txn != r.txn && !w.last {
					e.report("G1b", "%s read %s ending in %q, an intermediate append of %s", t, k, r.list[n-1], e.txns[w.txn])
				}
				e.edge(w.txn, r.txn, edgeWR, k)
			}
		}
		if n := len(r.list); n < len(order.list) {
			if w, ok := e.writers[element{k, order.list[n]}]; ok {
				e.edge(r.txn, w.txn, edgeRW, k)
			}
		}
	}
}

func sortedCopy(l []string) []string {
	c := slices.Clone(l)
	slices.Sort(c)
	return c
}

func hasPrefix(l, prefix []string) bool {
	return len(l) >= len(prefix) && slices.Equal(l[:len(prefix)], prefix)
}

func (e *elle) edge(from, to int, kind, key string) {
	if from == to || e.txns[from].op.Status == StatusFail || e.txns[to].op.Status == StatusFail {
		return
	}
	e.g.add(from, to, kind, key)
}

func (e *elle) findCycles() {
	ww := []string{edgeWW}
	wwr := []string{edgeWW, edgeWR}
	all := []string{edgeWW, edgeWR, edgeRW}
	for _, s := range []struct {
		kind    string
		first   string
		back    []string
		without []string
	}{{"G0", edgeWW, ww, nil}, {"G1c", edgeWR, wwr, nil}, {"G-single", edgeRW, wwr, nil}, {"G2", edgeRW, all, wwr}} {
		if cycle := e.g.cycle(s.first, s.back, s.without); cycle != nil {
			e.report(s.kind, "%s", e.describe(cycle))
		}
	}
}

func (e *elle) describe(cycle []dep) string {
	var b strings.Builder
	b.WriteString(e.txns[cycle[0].from].label())
	for _, s := range cycle {
		fmt.Fprintf(&b, " -%s %s-> %s", s.kind, s.key, e.txns[s.to].label())
	}
	for _, s := range cycle {
		fmt.Fprintf(&b, "; %s", e.txns[s.from])
	}
	return b.String()
}

type dep struct {
	from, to  int
	kind, key string
}

type graph struct {
	out  [][]dep
	seen map[dep]bool
}

func newGraph(n int) *graph { return &graph{out: make([][]dep, n), seen: map[dep]bool{}} }

func (g *graph) add(from, to int, kind, key string) {
	s := dep{from: from, to: to, kind: kind}
	if g.seen[s] {
		return
	}
	g.seen[s] = true
	s.key = key
	g.out[from] = append(g.out[from], s)
}

func (g *graph) components(kinds []string) []int {
	n := len(g.out)
	index, low, comp := make([]int, n), make([]int, n), make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i], comp[i] = -1, -1
	}
	var stack []int
	next, comps := 0, 0
	var visit func(v int)
	visit = func(v int) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, s := range g.out[v] {
			if !slices.Contains(kinds, s.kind) {
				continue
			}
			if index[s.to] < 0 {
				visit(s.to)
				low[v] = min(low[v], low[s.to])
			} else if onStack[s.to] {
				low[v] = min(low[v], index[s.to])
			}
		}
		if low[v] != index[v] {
			return
		}
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w], comp[w] = false, comps
			if w == v {
				break
			}
		}
		comps++
	}
	for v := range n {
		if index[v] < 0 {
			visit(v)
		}
	}
	return comp
}

func (g *graph) cycle(first string, back, without []string) []dep {
	comp := g.components(append(slices.Clone(back), first))
	for v := range g.out {
		for _, s := range g.out[v] {
			if s.kind != first || comp[s.from] != comp[s.to] {
				continue
			}
			if without != nil && g.path(s.to, s.from, without, comp) != nil {
				continue
			}
			if path := g.path(s.to, s.from, back, comp); path != nil {
				return append([]dep{s}, path...)
			}
		}
	}
	return nil
}

func (g *graph) path(from, to int, kinds []string, comp []int) []dep {
	via := map[int]dep{from: {}}
	queue := []int{from}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v == to {
			var path []dep
			for v != from {
				s := via[v]
				path = append(path, s)
				v = s.from
			}
			slices.Reverse(path)
			return path
		}
		for _, s := range g.out[v] {
			if _, seen := via[s.to]; seen || comp[s.to] != comp[from] || !slices.Contains(kinds, s.kind) {
				continue
			}
			via[s.to] = s
			queue = append(queue, s.to)
		}
	}
	return nil
}
