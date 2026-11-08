package engine

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func historyRunner(t *testing.T) *Runner {
	t.Helper()
	r := guestRunner(t, true)
	r.cfg.Workload.Args = append(r.cfg.Workload.Args, "-history")
	return r
}

func TestTimelineRecordsTheRunWithoutChangingIt(t *testing.T) {
	r := historyRunner(t)
	plain := r.Run(3, nil)
	in, err := r.Inspect(3, Schedule{}, InspectOptions{Timeline: true})
	if err != nil {
		t.Fatal(err)
	}
	res := in.Result
	if !slices.Equal(plain.Trace, res.Trace) || plain.Hash() != res.Hash() {
		t.Fatal("recording a timeline changed the run")
	}
	if plain.Timeline != nil {
		t.Fatal("a plain run recorded a timeline")
	}
	tl := res.Timeline
	if tl == nil || !slices.Equal(tl.Nodes, []string{"store", "client"}) {
		t.Fatalf("timeline nodes %+v", tl)
	}
	if len(tl.Logs) != len(res.Trace) {
		t.Fatalf("%d log entries for %d trace lines", len(tl.Logs), len(res.Trace))
	}
	for i, l := range tl.Logs {
		if l.Text != res.Trace[i] {
			t.Fatalf("log %d is %q, trace line is %q", i, l.Text, res.Trace[i])
		}
		if l.Output != "" && (l.Node == "" || !strings.Contains(l.Text, l.Output)) {
			t.Fatalf("guest output %+v lacks its node", l)
		}
	}
	kinds := map[string]int{}
	for _, m := range tl.Messages {
		kinds[m.Kind]++
		if m.Arrives < m.Sent || (m.Delivered() && m.Read < m.Arrives) {
			t.Fatalf("message %+v is read before it arrives", m)
		}
		if m.From == m.To || m.FromInstance == "" || m.ToInstance == "" {
			t.Fatalf("message %+v has no endpoints", m)
		}
	}
	if kinds[MessageConnect] == 0 || kinds[MessageData] == 0 {
		t.Fatalf("message kinds %v", kinds)
	}
	if len(tl.Ops) == 0 || tl.Ops[0].Node != "client" || tl.Ops[0].Instance == "" {
		t.Fatalf("ops %+v", tl.Ops)
	}
	starts := 0
	for _, l := range tl.Lives {
		if l.Start > tl.End || (l.End >= 0 && l.End < l.Start) {
			t.Fatalf("life %+v", l)
		}
		if l.Node == "store" {
			starts++
		}
	}
	crashes := 0
	for _, f := range res.FaultsApplied() {
		if f.Kind == FaultCrash || f.Kind == FaultCrashIO {
			crashes++
		}
	}
	if starts < 1+min(crashes, 1) || len(tl.Faults) != len(res.FaultsApplied()) {
		t.Fatalf("%d store lives and %d fault spans for %d crashes and %d faults", starts, len(tl.Faults), crashes, len(res.FaultsApplied()))
	}
	if !slices.ContainsFunc(tl.Marks, func(m Mark) bool { return m.Kind == MarkEvent }) || !slices.ContainsFunc(tl.Marks, func(m Mark) bool { return m.Kind == MarkProperty }) {
		t.Fatalf("marks %+v", tl.Marks)
	}
}

func TestTimelineRendersOfflineHTMLAndMermaid(t *testing.T) {
	r := historyRunner(t)
	in, err := r.Inspect(3, Schedule{}, InspectOptions{Timeline: true})
	if err != nil {
		t.Fatal(err)
	}
	var page, mmd bytes.Buffer
	if err := in.Result.Timeline.WriteHTML(&page, "guest seed 3"); err != nil {
		t.Fatal(err)
	}
	html := page.String()
	for _, want := range []string{"guest seed 3", `"messages":[{`, `"logs":[{`, "new RegExp"} {
		if !strings.Contains(html, want) {
			t.Fatalf("timeline page lacks %q", want)
		}
	}
	if regexp.MustCompile(`(?i)<script[^>]+src=|<link[^>]+href=|fetch\(|https?://[^"]*\.(js|css)`).MatchString(html) {
		t.Fatal("timeline page loads something from the network")
	}
	if err := in.Result.Timeline.WriteMermaid(&mmd); err != nil {
		t.Fatal(err)
	}
	text := mmd.String()
	for _, want := range []string{"sequenceDiagram\n", "participant p0 as store", "participant p1 as client", "p1->>p0: ", "Note over p0: 0.000s start store#35;1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("mermaid lacks %q:\n%s", want, text[:min(len(text), 600)])
		}
	}
	statement := regexp.MustCompile(`^  (participant p\d+ as ([^#;]|#\d+;)+|p\d+(->>|-->>|-x)p\d+: ([^#;]|#\d+;)+|Note over p\d+(,p\d+)*: ([^#;]|#\d+;)+)$`)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n")[1:] {
		if !statement.MatchString(line) {
			t.Fatalf("malformed mermaid line %q", line)
		}
	}
}

func TestNetTraceAddsLinesWithoutChangingTheRun(t *testing.T) {
	r := guestRunner(t, true)
	plain := r.Run(3, nil)
	r.NetTrace = true
	a, b := r.Run(3, nil), r.Run(3, nil)
	if !slices.Equal(a.Trace, b.Trace) {
		t.Fatal("two -net-trace runs differ")
	}
	if a.Hash() != plain.Hash() {
		t.Fatal("-net-trace changed the host calls")
	}
	var net, rest []string
	for _, l := range a.Trace {
		if strings.Contains(l, "  net ") {
			net = append(net, l)
			continue
		}
		rest = append(rest, l)
	}
	if !slices.Equal(rest, plain.Trace) {
		t.Fatal("-net-trace changed the other trace lines")
	}
	joined := strings.Join(net, "\n")
	for _, want := range []string{"dials store:7000 conn", "accepts conn", "-> store#1 conn", "<- client#1 conn", "bytes sent at"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("net trace lacks %q:\n%s", want, joined[:min(len(joined), 800)])
		}
	}
}

func TestDebugSessionMatchesInspectAndSteps(t *testing.T) {
	r := guestRunner(t, true)
	at := 300 * time.Millisecond
	in, err := r.Inspect(3, Schedule{}, InspectOptions{At: at})
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Debug(3, Schedule{}, at)
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if st.At > at || st.At != in.At || st.Over {
		t.Fatalf("paused at %s (over %v), inspect snapshot at %s", st.At, st.Over, in.At)
	}
	d, err := s.Disk("store")
	if err != nil {
		t.Fatal(err)
	}
	want := in.Disks[slices.IndexFunc(in.Disks, func(d Disk) bool { return d.Node == "store" })]
	if d.Used != want.Used || len(d.Files) != len(want.Files) {
		t.Fatalf("debug disk %+v, inspect disk %+v", d, want)
	}
	if _, _, err := s.File("store", "nope"); err == nil {
		t.Fatal("a missing file has no error")
	}
	if _, err := s.Disk("nobody"); err == nil {
		t.Fatal("an unknown node has no error")
	}
	if len(st.Nodes) != 2 || st.Nodes[0].Node != "store" {
		t.Fatalf("nodes %+v", st.Nodes)
	}
	before := st.Steps
	if !s.Step(5) || s.State().Steps != before+5 {
		t.Fatalf("step 5 from %d reached %d", before, s.State().Steps)
	}
	line, ok := s.Until(regexp.MustCompile(`fault `))
	if !ok || !strings.Contains(line, "fault ") {
		t.Fatalf("until found %q, %v", line, ok)
	}
	last := s.Trace()
	if last[len(last)-1].Text != line {
		t.Fatalf("until stopped after %q, not at the match", last[len(last)-1].Text)
	}
	s.InFlight()
	s.Faults()
	if err := s.Branch(7); err != nil {
		t.Fatal(err)
	}
	s.Step(1)
	if err := s.Branch(8); err == nil {
		t.Fatal("a second branch has no error")
	}
	if !slices.ContainsFunc(s.Trace(), func(l TraceLine) bool { return strings.Contains(l.Text, "branch 7 taken") }) {
		t.Fatal("the branch was not taken")
	}
	for s.Step(1000) {
	}
	if !s.State().Over || s.Step(1) {
		t.Fatal("the session does not end")
	}
	s.Close()
}

func TestDebugCloseMidRunAndPastTheEnd(t *testing.T) {
	r := guestRunner(t, true)
	s, err := r.Debug(1, Schedule{}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = r.Debug(1, Schedule{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	full := r.Run(1, nil)
	if st := s.State(); !st.Over || st.At != full.Duration || st.Failed != full.Failed {
		t.Fatalf("debug to the end: %+v, run ended at %s failed %v", st, full.Duration, full.Failed)
	}
	if _, next := s.Faults(); next != nil {
		t.Fatalf("an ended run has a next fault %+v", next)
	}
	if got := s.Close(); !slices.Equal(got.Trace, full.Trace) {
		t.Fatal("a debug session to the end differs from a run")
	}
	s, err = r.Debug(1, Schedule{}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	s.Step(3)
	s.RunTo(time.Hour)
	if got := s.Close(); !slices.Equal(got.Trace, full.Trace) || got.Hash() != full.Hash() {
		t.Fatal("pausing and stepping changed the run")
	}
	if _, err := r.Debug(1, Schedule{}, -time.Second); err == nil {
		t.Fatal("a negative time has no error")
	}
}

func TestDebugNetListsDataSentBeforeAccept(t *testing.T) {
	r := guestRunner(t, true)
	s, err := r.Debug(3, Schedule{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for s.Step(1) {
		pending := map[int32]bool{}
		for _, m := range s.InFlight() {
			if m.Kind == MessageConnect {
				pending[m.Conn] = true
			}
		}
		for _, m := range s.InFlight() {
			if m.Kind != MessageConnect && pending[m.Conn] {
				return
			}
		}
	}
	t.Fatal("no data sent before its connection was accepted was listed in flight")
}
