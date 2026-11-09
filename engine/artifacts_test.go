package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestSeedsFileReadsSeedsAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seeds.txt")
	os.WriteFile(path, []byte("# known-bad seeds\n7\n12 40  # torn tail\n\n"), 0o644)
	regs, err := SeedsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var seeds []uint64
	for _, r := range regs {
		seeds = append(seeds, r.File.Seed)
		if r.File.Faults != nil || r.Path != "" {
			t.Fatalf("a listed seed must run with its own faults: %+v", r)
		}
	}
	if !slices.Equal(seeds, []uint64{7, 12, 40}) || regs[1].Source != path+":3" {
		t.Fatalf("seeds %v, sources %+v", seeds, regs)
	}
	os.WriteFile(path, []byte("7\nseven\n"), 0o644)
	if _, err := SeedsFile(path); err == nil {
		t.Fatal("a word in a seeds file must be an error")
	}
}

func TestPinnedKeepsOnlyThisConfigsSchedules(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Dir: dir, Path: filepath.Join(dir, "seedfall.json")}
	other := &Config{Dir: dir, Path: filepath.Join(dir, "seedfall.fixed.json")}
	mine := ScheduleFile{Seed: 3, Schedule: Schedule{Faults: []Fault{}}, Pinned: &Pin{Config: "seedfall.json"}}
	theirs := ScheduleFile{Seed: 3, Schedule: Schedule{Faults: []Fault{}}, Pinned: &Pin{Config: "seedfall.fixed.json"}}
	shared := ScheduleFile{Seed: 9, Schedule: Schedule{Faults: []Fault{}}}
	for path, f := range map[string]ScheduleFile{PinPath(cfg, 3): mine, PinPath(other, 3): theirs, filepath.Join(dir, RegressionsDir, "copied.json"): shared} {
		if err := WriteScheduleFile(path, f); err != nil {
			t.Fatal(err)
		}
	}
	if filepath.Base(PinPath(cfg, 3)) == filepath.Base(PinPath(other, 3)) {
		t.Fatal("two configs pinning the same seed must not share a file")
	}
	regs, err := Pinned(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var seeds []uint64
	for _, r := range regs {
		seeds = append(seeds, r.File.Seed)
		if r.Schedule().Duration != 0 {
			t.Fatal("a regression must replay for the config's full duration")
		}
	}
	slices.Sort(seeds)
	if !slices.Equal(seeds, []uint64{3, 9}) {
		t.Fatalf("pinned seeds %v, want this config's 3 and the unlabeled 9", seeds)
	}
}

func TestGuestArtifactsReplayExactly(t *testing.T) {
	r := guestRunner(t, false)
	failed := firstFailure(t, r)
	dir := t.TempDir()
	r.cfg.Dir, r.cfg.Path = dir, filepath.Join(dir, "seedfall.json")
	if err := os.WriteFile(r.cfg.Path, []byte(`{"workload": {"name": "client", "pkg": "."}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a, res, err := r.SaveArtifacts(failed.Seed, Schedule{}, ArtifactOptions{Config: "seedfall.json", Original: &failed})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Reproduced || a.Classification.Verdict != VerdictBug || !reflect.DeepEqual(res.Trace, failed.Trace) {
		t.Fatalf("artifact run: reproduced %v, class %s", a.Reproduced, a.Classification)
	}
	for _, name := range []string{ArtifactReport, ArtifactTrace, ArtifactFaults, filepath.Join(ArtifactDisks, "store")} {
		if _, err := os.Stat(filepath.Join(a.Dir, name)); err != nil {
			t.Errorf("artifact %s: %v", name, err)
		}
	}
	var saved Artifact
	b, _ := os.ReadFile(filepath.Join(a.Dir, ArtifactReport))
	if err := json.Unmarshal(b, &saved); err != nil || saved.Finding != failed.Failure.Key() || saved.Version == "" || len(saved.ConfigSHA256) != 64 || saved.Seed != failed.Seed {
		t.Fatalf("report.json %s (%v)", b, err)
	}
	file, err := ReadScheduleFile(filepath.Join(a.Dir, ArtifactFaults))
	if err != nil {
		t.Fatal(err)
	}
	if again := r.Replay(file.Seed, file.Schedule); !reflect.DeepEqual(again.Trace, failed.Trace) {
		t.Fatal("faults.json from the artifacts does not replay the failure exactly")
	}
	pin := Regression{Path: "pinned.json", Label: "pinned", File: file}
	got := ReplayRegressions(r, []Regression{pin, {File: ScheduleFile{Seed: failed.Seed}}}, 2)
	if bad := FirstFailed(got); bad == nil || bad.Path != "pinned.json" || !got[1].Result.sameFailure(failed) {
		t.Fatal("a pinned schedule and a listed seed of a failing run must both fail again")
	}
	if pin.ArtifactOptions("c", nil).Label != "pinned" || (Regression{}).ArtifactOptions("c", nil).Label != "" {
		t.Fatal("pinned artifacts must not overwrite the seed's own")
	}
}

func TestArtifactDirsDoNotCollideAcrossConfigs(t *testing.T) {
	dir := t.TempDir()
	a := &Config{Dir: dir, Path: filepath.Join(dir, "seedfall.always.json")}
	b := &Config{Dir: dir, Path: filepath.Join(dir, "seedfall.bitrot.json")}
	if ArtifactDir(a, 1, "") == ArtifactDir(b, 1, "") || ArtifactDir(a, 1, "") == ArtifactDir(a, 1, "pinned") {
		t.Fatal("two configs, or a seed and its pinned schedule, must not share an artifacts folder")
	}
	if got, want := ArtifactDir(a, 1, "shrunk"), filepath.Join(dir, ".seedfall", "failures", "seedfall.always-seed-1-shrunk"); got != want {
		t.Fatalf("ArtifactDir = %s, want %s", got, want)
	}
}

func TestReplayCommandQuotesTheFaultsPath(t *testing.T) {
	if got, want := ReplayCommand("seedfall.json", 3, filepath.Join("out", "faults.json")), "seedfall replay -c seedfall.json -seed 3 -faults out/faults.json"; got != want {
		t.Errorf("ReplayCommand = %s, want %s", got, want)
	}
	if got, want := ReplayCommand("seedfall.json", 3, filepath.Join("my dir", "it's", "faults.json")), `seedfall replay -c seedfall.json -seed 3 -faults 'my dir/it'\''s/faults.json'`; got != want {
		t.Errorf("ReplayCommand = %s, want %s", got, want)
	}
}
