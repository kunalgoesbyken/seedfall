package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

func TestScheduleFilesRoundTripAndOldArraysStillRead(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "seedfall.json")
	shrunk := engine.Shrunk{
		Schedule: engine.Schedule{
			Faults:   []engine.Fault{{At: engine.Duration(427 * time.Millisecond), Kind: engine.FaultCrashIO, Node: "server", Op: 56, Until: engine.Duration(761 * time.Millisecond)}},
			Duration: engine.Duration(991 * time.Millisecond),
			Ops:      63,
		},
		Before: engine.ScheduleSize{Faults: 33, Duration: engine.Duration(time.Minute), Ops: 200},
		Runs:   191,
	}
	path, err := saveSchedule(config, 1, shrunk)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readSchedule(path)
	if err != nil || !reflect.DeepEqual(got, shrunk.Schedule) {
		t.Fatalf("read back %+v (%v), want %+v", got, err, shrunk.Schedule)
	}
	var file map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &file); err != nil || file["shrunk_from"] == nil || file["shrink_runs"] != float64(191) || file["seed"] != float64(1) {
		t.Fatalf("saved file lacks the original size, run count or seed: %s", b)
	}

	legacy := filepath.Join(dir, "old.faults.json")
	os.WriteFile(legacy, []byte(` [{"at": "1s", "kind": "crash", "node": "n1", "until": "2s"}]`), 0o644)
	old, err := readSchedule(legacy)
	if err != nil || len(old.Faults) != 1 || old.Duration != 0 || old.Ops != 0 {
		t.Fatalf("a fault array read as %+v (%v)", old, err)
	}
	empty := filepath.Join(dir, "empty.faults.json")
	os.WriteFile(empty, []byte(`{"seed": 3}`), 0o644)
	if none, err := readSchedule(empty); err != nil || none.Faults == nil {
		t.Fatalf("a schedule without faults must replay with no faults, not the seed's own: %+v (%v)", none, err)
	}
}

func TestParseBudget(t *testing.T) {
	tests := []struct {
		in      string
		runs    int
		time    time.Duration
		invalid bool
	}{
		{"", 0, 0, false},
		{"300", 300, 0, false},
		{"10m", 0, 10 * time.Minute, false},
		{"0", 0, 0, true},
		{"-5", 0, 0, true},
		{"-1m", 0, 0, true},
		{"soon", 0, 0, true},
	}
	for _, tt := range tests {
		opts, err := parseBudget(tt.in)
		if (err != nil) != tt.invalid || opts.MaxRuns != tt.runs || opts.MaxTime != tt.time {
			t.Errorf("parseBudget(%q) = %+v, %v", tt.in, opts, err)
		}
	}
}

func TestMCPShrinkTakesABudget(t *testing.T) {
	cs := connect(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "seedfall_shrink" {
			continue
		}
		in, _ := json.Marshal(tl.InputSchema)
		out, _ := json.Marshal(tl.OutputSchema)
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		json.Unmarshal(in, &schema)
		if schema.Properties["budget"] == nil {
			t.Errorf("seedfall_shrink input has no budget: %s", in)
		}
		json.Unmarshal(out, &schema)
		for _, field := range []string{"original", "minimal", "runs", "budget_exhausted", "schedule_file"} {
			if schema.Properties[field] == nil {
				t.Errorf("seedfall_shrink output has no %s: %s", field, out)
			}
		}
		return
	}
	t.Fatal("seedfall_shrink is not listed")
}
