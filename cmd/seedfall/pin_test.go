package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
)

func TestPinSourceRejectsAScheduleForAnotherSeed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seed-4.faults.json")
	if err := engine.WriteScheduleFile(path, engine.ScheduleFile{Seed: 4, Schedule: engine.Schedule{Faults: []engine.Fault{}, Ops: 12}}); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "seedfall.json")
	if _, _, err := pinSource(nil, pinArgs{Config: config, Seed: 5, Faults: path}); err == nil || !strings.Contains(err.Error(), "seed 4, not 5") {
		t.Fatalf("pinning seed 5 from a seed 4 schedule: %v", err)
	}
	file, from, err := pinSource(nil, pinArgs{Config: config, Seed: 4, Faults: path})
	if err != nil || file.Seed != 4 || file.Ops != 12 || from != filepath.ToSlash(path) {
		t.Fatalf("pinSource = %+v, %q, %v", file, from, err)
	}
}
