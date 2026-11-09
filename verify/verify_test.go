package verify

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestSeedsNeeded(t *testing.T) {
	tests := []struct {
		name     string
		targets  []Target
		seeds    int
		baseRate float64
	}{
		{"no known rate falls back to a 1% failure", []Target{{Label: "seed 9"}}, report.SeedsToConfirm(report.RareRate), 0},
		{"the rarest known failure decides", []Target{{Rate: 0.1}, {Rate: 0.05}, {}}, 59, 0.05},
		{"a single common failure", []Target{{Rate: 0.5}}, 5, 0.5},
	}
	for _, tt := range tests {
		seeds, rate := SeedsNeeded(tt.targets)
		if seeds != tt.seeds || rate != tt.baseRate {
			t.Errorf("%s: SeedsNeeded = %d, %v; want %d, %v", tt.name, seeds, rate, tt.seeds, tt.baseRate)
		}
	}
}

func bug(seed uint64, reason string) *Failure {
	return &Failure{Seed: seed, Reason: reason, Classification: engine.Classification{Verdict: engine.VerdictBug}}
}

func TestProofRendering(t *testing.T) {
	sweep := &Sweep{Seeds: "1-299", Count: 299, Passed: 299, Rate: 0.0167, Detectable: report.DetectableRate(299)}
	proofs := map[string]Proof{
		"pass": {
			Config: "seedfall.json", Version: "v0.2.0", Command: "seedfall verify -seed 9", Passed: true,
			Replays:     []Replay{{Target: "seed 9", Seed: 9}},
			Sweep:       sweep,
			Determinism: &Sweep{Seeds: "1-20", Count: 20, Passed: 20},
			Mutants: []Mutant{
				{Mutation: engine.FsyncNoop, Outcome: engine.MutantKilled, Seed: 4},
				{Mutation: engine.DirsyncNoop, Outcome: engine.MutantKilled, Seed: 7},
			},
		},
		"mutant-survives": {
			Config: "seedfall.json", Version: "v0.2.0", Command: "seedfall verify -seed 9 -mutate",
			Replays:     []Replay{{Target: "seed 9", Seed: 9}},
			Sweep:       sweep,
			Determinism: &Sweep{Seeds: "1-20", Count: 20, Passed: 20},
			Mutants: []Mutant{
				{Mutation: engine.DirsyncNoop, Outcome: engine.MutantSurvived},
				{Location: "server/main.go:197", Change: `cmd[0] == "PUT" -> cmd[0] != "PUT"`, Outcome: engine.MutantSurvived},
				{Location: "server/main.go:157", Change: "err != nil -> err == nil", Outcome: engine.MutantKilled, Seed: 1},
			},
		},
		"mutant-error": {
			Config: "seedfall.json", Version: "v0.2.0", Command: "seedfall verify -seed 9 -app-mutants 3",
			Replays:     []Replay{{Target: "seed 9", Seed: 9}},
			Sweep:       sweep,
			Determinism: &Sweep{Seeds: "1-20", Count: 20, Passed: 20},
			MutantError: "go list ./server: exit status 1",
		},
		"replay-fails": {
			Config: "seedfall.json", Version: "v0.2.0", Command: "seedfall verify -seed 9",
			Replays: []Replay{{Target: "seed 9", Seed: 9, Failure: bug(9, "client#1: acknowledged write lost | k3")}},
		},
		"sweep-fails": {
			Config: "seedfall.json", Version: "v0.2.0", Command: "seedfall verify -seed 9",
			Replays: []Replay{{Target: "seed 9", Seed: 9}},
			Sweep:   &Sweep{Seeds: "1-299", Count: 299, Passed: 40, Failure: bug(41, "server#2 crashed: panic")},
		},
	}
	for name, p := range proofs {
		for ext, text := range map[string]string{"txt": p.Text(), "md": p.Markdown()} {
			golden(t, filepath.Join("testdata", name+"."+ext), text)
		}
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), []byte(got)) {
		t.Errorf("%s differs; rerun with -update and review the diff\n%s", path, got)
	}
}

func TestDoublecheckedTurnsTheCheckOnOnlyForTheCall(t *testing.T) {
	r := &engine.Runner{}
	if !doublechecked(r, func() bool { return r.Doublecheck }) {
		t.Fatal("the runner was not doublechecking inside the call")
	}
	if r.Doublecheck {
		t.Fatal("the runner kept doublechecking after the call")
	}
}

func TestMutantOfRecordsTheSeedThatKilledIt(t *testing.T) {
	base := Mutant{Location: "a.go:3", Change: "x -> y"}
	killed := mutantOf(base, engine.MutantKilled, &engine.Result{Seed: 7, Reason: "lost write"})
	if killed.Outcome != engine.MutantKilled || killed.Seed != 7 || killed.Reason != "lost write" || killed.Location != base.Location || killed.Change != base.Change {
		t.Fatalf("killed mutant %+v", killed)
	}
	if survived := mutantOf(base, engine.MutantSurvived, nil); survived.Outcome != engine.MutantSurvived || survived.Seed != 0 || survived.Reason != "" {
		t.Fatalf("survived mutant %+v", survived)
	}
}
