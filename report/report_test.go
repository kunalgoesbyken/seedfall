package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestSeedsToConfirm(t *testing.T) {
	tests := []struct {
		rate float64
		want int
	}{
		{0.01, 299},
		{0.05, 59},
		{0.1, 29},
		{0.5, 5},
		{1, 1},
	}
	for _, tt := range tests {
		got := SeedsToConfirm(tt.rate)
		if got != tt.want {
			t.Errorf("SeedsToConfirm(%v) = %d, want %d", tt.rate, got, tt.want)
		}
		if math.Pow(1-tt.rate, float64(got)) > confidence {
			t.Errorf("%d clean seeds leave a %v failure unseen with probability above %v", got, tt.rate, confidence)
		}
		if got > 1 && math.Pow(1-tt.rate, float64(got-1)) <= confidence {
			t.Errorf("SeedsToConfirm(%v) = %d is not the smallest sufficient count", tt.rate, got)
		}
	}
}

func TestDetectableRate(t *testing.T) {
	tests := []struct {
		seeds int
		want  float64
	}{
		{1, 0.95},
		{100, 0.029513},
		{299, 0.009970},
		{1000, 0.002991},
	}
	for _, tt := range tests {
		if got := DetectableRate(tt.seeds); math.Abs(got-tt.want) > 1e-6 {
			t.Errorf("DetectableRate(%d) = %.6f, want %.6f", tt.seeds, got, tt.want)
		}
	}
	for _, rate := range []float64{0.001, 0.01, 0.2} {
		if got := DetectableRate(SeedsToConfirm(rate)); got > rate {
			t.Errorf("after SeedsToConfirm(%v) clean seeds the detectable rate is %v, want at most %v", rate, got, rate)
		}
	}
}

var lost = engine.Failure{Class: engine.FailAssert, Node: "client"}

func failing(seed uint64) engine.Result {
	return engine.Result{
		Seed: seed, Failed: true, Failure: lost, Reason: "client#1: acknowledged write lost", At: 2 * time.Second,
		Faults: []engine.Fault{
			{At: engine.Duration(time.Second), Kind: engine.FaultCrash, Node: "server", Until: engine.Duration(1500 * time.Millisecond)},
			{At: engine.Duration(3 * time.Second), Kind: engine.FaultPause, Node: "server", Until: engine.Duration(4 * time.Second)},
		},
		Trace: []string{"      0.000  start server#1", "      2.000  FAIL  client#1: acknowledged write lost"},
	}
}

func input(seeds string, passed int, failures ...engine.Result) Input {
	byseed := map[uint64]engine.Result{}
	for _, f := range failures {
		byseed[f.Seed] = f
	}
	return Input{
		Config: "seedfall.json", Seeds: seeds,
		Summary: engine.Summary{Passed: passed, Failures: failures, Coverage: engine.Coverage{
			"a crash interrupted the workload": {Kind: engine.Sometimes, SeedsReached: passed + len(failures), SeedsTrue: 3},
			"never runs":                       {Kind: engine.Reachable},
		}},
		Elapsed: 1234 * time.Millisecond,
		Replay: func(seed uint64) Replayed {
			return Replayed{Result: byseed[seed], Class: engine.Classification{Verdict: engine.VerdictBug}, Artifacts: fmt.Sprintf(".seedfall/failures/seedfall-seed-%d", seed)}
		},
	}
}

func TestBuildMatchesGolden(t *testing.T) {
	crash := engine.Result{Seed: 4, Failed: true, Failure: engine.Failure{Class: engine.FailCrash, Node: "server"}, Reason: "server#2 crashed: panic"}
	r := Build(input("1-100", 96, failing(9), crash, failing(3), failing(30)), map[string]known{})
	r.Generated, r.GoVersion, r.Version = time.Time{}, "go", "v0.0.0"
	got, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "report.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("report differs from %s; rerun with -update and review the diff\n%s", golden, got)
	}
}

func TestFindingStatusAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	history := filepath.Join(dir, "history.jsonl")
	key := lost.Key()
	steps := []struct {
		name    string
		in      Input
		status  Status
		cleared Status
	}{
		{"first sighting", input("1-100", 90, failing(1), failing(2), failing(3), failing(4), failing(5), failing(6), failing(7), failing(8), failing(9), failing(10)), StatusNew, ""},
		{"seen again", input("101-200", 99, failing(150)), StatusOngoing, ""},
		{"clean, too few seeds", input("201-400", 200), "", StatusUnconfirmed},
		{"clean, enough seeds", input("401-500", 100), "", StatusResolved},
		{"clean after resolution", input("501-600", 100), "", ""},
		{"back after resolution", input("601-700", 99, failing(650)), StatusRegressed, ""},
	}
	for i, s := range steps {
		r, err := Generate(s.in, filepath.Join(dir, "report.html"), history)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		var status, cleared Status
		for _, f := range r.Findings {
			if f.Key == key {
				status = f.Status
			}
		}
		for _, f := range r.Cleared {
			if f.Key == key {
				cleared = f.Status
			}
		}
		if status != s.status || cleared != s.cleared {
			t.Fatalf("step %d (%s): finding %q, cleared %q; want %q, %q", i+1, s.name, status, cleared, s.status, s.cleared)
		}
		loaded, err := Load(filepath.Join(dir, "report.html"))
		if err != nil || loaded.Total != r.Total || len(loaded.Findings) != len(r.Findings) {
			t.Fatalf("%s: reloaded report %+v, %v", s.name, loaded, err)
		}
	}
}

func TestReportEmbedsTheArtifactTimeline(t *testing.T) {
	dir := t.TempDir()
	artifacts := filepath.Join(dir, ".seedfall", "failures", "seedfall-seed-3")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, engine.ArtifactRun), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := input("1-10", 8, failing(3), failing(4))
	in.Replay = func(seed uint64) Replayed {
		return Replayed{Result: failing(seed), Artifacts: filepath.ToSlash(filepath.Join(dir, ".seedfall", "failures", fmt.Sprintf("seedfall-seed-%d", seed)))}
	}
	path := filepath.Join(dir, "out", "report.html")
	r, err := Generate(in, path, filepath.Join(dir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Findings[0].Example.Timeline; got != "../.seedfall/failures/seedfall-seed-3/timeline.html" {
		t.Fatalf("timeline link %q", got)
	}
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte(`<iframe class="timeline" src="../.seedfall/failures/seedfall-seed-3/timeline.html"`)) {
		t.Fatal("the report does not embed the timeline")
	}
	in.Replay = func(seed uint64) Replayed {
		return Replayed{Result: failing(seed), Artifacts: filepath.ToSlash(filepath.Join(dir, "nowhere"))}
	}
	if r, _ := Generate(in, path, filepath.Join(dir, "history.jsonl")); r.Findings[0].Example.Timeline != "" {
		t.Fatal("a missing timeline was linked")
	}
}
