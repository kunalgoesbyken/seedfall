package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const baseConfig = `{
  "nodes": [
    {"name": "a", "pkg": "./server", "args": ["-sync", "batch"], "env": {"K": "1"}},
    {"name": "b", "pkg": "./server"}
  ],
  "workload": {"name": "client", "pkg": "./client"},
  "duration": "60s",
  "faults": {"crash_per_sec": 0.5, "torn_writes": true},
  "profiles": {
    "always": {"nodes": [{"name": "a", "args": ["-sync", "always"]}]},
    "calm": {"faults": "none"},
    "more": {"faults": {"partition_per_sec": 1}}
  }
}`

func TestExtendsMergesObjectsAndNamedLists(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"base.json": baseConfig,
		"child.json": `{"extends": "base.json", "duration": "5s",
		  "nodes": [{"name": "a", "env": {"L": "2"}}, {"name": "c", "pkg": "./server"}],
		  "faults": {"crash_on_io": 0.1}}`,
	})
	cfg, err := LoadConfig(filepath.Join(dir, "child.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(cfg.Duration); got != 5*time.Second {
		t.Errorf("duration = %s, want 5s", got)
	}
	names := []string{}
	for _, n := range cfg.Nodes {
		names = append(names, n.Name)
	}
	if !slices.Equal(names, []string{"a", "b", "c"}) {
		t.Fatalf("nodes = %v, want a b c", names)
	}
	a := cfg.Nodes[0]
	if !slices.Equal(a.Args, []string{"-sync", "batch"}) || a.Env["K"] != "1" || a.Env["L"] != "2" {
		t.Errorf("node a = %+v, want inherited args and both env keys", a)
	}
	if cfg.Faults.CrashPerSec != 0.5 || !cfg.Faults.TornWrites || cfg.Faults.CrashOnIO != 0.1 {
		t.Errorf("faults = %+v, want inherited crash and torn writes plus crash_on_io", cfg.Faults)
	}
	if cfg.Faults.RestartAfter != defaultConfig().Faults.RestartAfter {
		t.Errorf("restart_after = %v, want the default", cfg.Faults.RestartAfter)
	}
}

func TestExtendsCycleIsAnError(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.json": `{"extends": "b.json"}`,
		"b.json": `{"extends": "a.json"}`,
	})
	if _, err := LoadConfig(filepath.Join(dir, "a.json")); err == nil || !strings.Contains(err.Error(), "extends itself") {
		t.Fatalf("err = %v, want a cycle error", err)
	}
}

func TestProfilesMergeOverTheBase(t *testing.T) {
	dir := writeFiles(t, map[string]string{"seedfall.json": baseConfig})
	path := filepath.Join(dir, "seedfall.json")
	cfg, err := Load(ConfigRef{Path: path, Profile: "always"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Nodes[0].Args, []string{"-sync", "always"}) || cfg.Nodes[0].Env["K"] != "1" || len(cfg.Nodes) != 2 {
		t.Errorf("nodes = %+v, want a with -sync always and its env kept", cfg.Nodes)
	}
	if cfg.Stem() != "seedfall.always" {
		t.Errorf("stem = %q, want seedfall.always", cfg.Stem())
	}
	more, err := Load(ConfigRef{Path: path, Profile: "more"})
	if err != nil {
		t.Fatal(err)
	}
	if more.Faults.CrashPerSec != 0.5 || more.Faults.PartitionPerSec != 1 {
		t.Errorf("faults = %+v, want the base crash rate and the profile's partitions", more.Faults)
	}
	_, err = Load(ConfigRef{Path: path, Profile: "nope"})
	if err == nil || !strings.Contains(err.Error(), "have always, calm, more") {
		t.Errorf("err = %v, want the known profiles listed", err)
	}
}

func TestPresetInALayerReplacesInheritedFaults(t *testing.T) {
	dir := writeFiles(t, map[string]string{"seedfall.json": baseConfig})
	cfg, err := Load(ConfigRef{Path: filepath.Join(dir, "seedfall.json"), Profile: "calm"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Faults.CrashPerSec != 0 || cfg.Faults.TornWrites || cfg.Faults.Preset != "none" {
		t.Errorf("faults = %+v, want none", cfg.Faults)
	}
}

func TestPresetsAreCumulativeAndExplicitFieldsWin(t *testing.T) {
	dir := writeFiles(t, map[string]string{"seedfall.json": `{
	  "nodes": [{"name": "a", "pkg": "./server"}], "workload": {"name": "client", "pkg": "./client"},
	  "faults": {"preset": "high_availability", "crash_per_sec": 2, "clog_per_sec": 0}}`})
	cfg, err := LoadConfig(filepath.Join(dir, "seedfall.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.Faults
	if f.CrashPerSec != 2 || f.ClogPerSec != 0 || !f.TornWrites || f.PausePerSec == 0 || f.PartitionPerSec == 0 || f.FsyncEIO != 0 {
		t.Errorf("faults = %+v, want durability and resilient faults, partitions, crash_per_sec 2 and no clogs or disk errors", f)
	}
	for _, p := range FaultPresets {
		spec, err := presetFaults(defaultConfig().Faults, p.Name)
		if err != nil || spec.Preset != p.Name {
			t.Errorf("preset %s: %+v, %v", p.Name, spec, err)
		}
	}
	if _, err := presetFaults(defaultConfig().Faults, "chaos"); err == nil || !strings.Contains(err.Error(), "none, durability, resilient, high_availability, everything") {
		t.Errorf("err = %v, want the presets listed", err)
	}
}

func TestSetOverridesValuesLast(t *testing.T) {
	dir := writeFiles(t, map[string]string{"seedfall.json": baseConfig})
	ref := ConfigRef{Path: filepath.Join(dir, "seedfall.json"), Profile: "always", Sets: []string{
		"duration=2s", "faults.crash_per_sec=1.5", `nodes.a.args=["-sync","never"]`, "nodes.1.env.MODE=fast", "watchdog=5s",
	}}
	cfg, err := Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(cfg.Duration) != 2*time.Second || cfg.Faults.CrashPerSec != 1.5 || time.Duration(cfg.Watchdog) != 5*time.Second {
		t.Errorf("duration %s, crash %v, watchdog %s", time.Duration(cfg.Duration), cfg.Faults.CrashPerSec, time.Duration(cfg.Watchdog))
	}
	if !slices.Equal(cfg.Nodes[0].Args, []string{"-sync", "never"}) || cfg.Nodes[1].Env["MODE"] != "fast" {
		t.Errorf("nodes = %+v", cfg.Nodes)
	}
	preset, err := Load(ConfigRef{Path: ref.Path, Sets: []string{"faults=durability", "faults.crash_per_sec=3"}})
	if err != nil {
		t.Fatal(err)
	}
	if preset.Faults.Preset != "durability" || preset.Faults.CrashPerSec != 3 || !preset.Faults.StrictDirs {
		t.Errorf("faults = %+v, want durability with crash_per_sec 3", preset.Faults)
	}
	kept, err := Load(ConfigRef{Path: ref.Path, Sets: []string{"faults.crash_per_sec=1.25", "faults.preset=durability"}})
	if err != nil {
		t.Fatal(err)
	}
	if kept.Faults.Preset != "durability" || kept.Faults.CrashPerSec != 1.25 || !kept.Faults.StrictDirs || !kept.Faults.TornWrites {
		t.Errorf("faults = %+v, want durability under the base config's fault fields", kept.Faults)
	}
	for _, bad := range []string{"duration", "nodes.z.args=[]", "duration.x=1"} {
		if _, err := Load(ConfigRef{Path: ref.Path, Sets: []string{bad}}); err == nil {
			t.Errorf("-set %s: want an error", bad)
		}
	}
}

func TestConfigRefStringIsACommandLine(t *testing.T) {
	ref := ConfigRef{Path: "seedfall.json", Profile: "always", Sets: []string{"faults.crash_per_sec=1", `nodes.a.args=["-x", "y"]`}}
	want := `seedfall.json -profile always -set faults.crash_per_sec=1 -set 'nodes.a.args=["-x", "y"]'`
	if got := ref.String(); got != want {
		t.Errorf("String() = %s, want %s", got, want)
	}
	if got := (ConfigRef{Path: "seedfall.json"}).String(); got != "seedfall.json" {
		t.Errorf("String() = %s, want the bare path", got)
	}
}

func TestPinsMatchTheirProfile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"seedfall.json": baseConfig})
	cfg, err := Load(ConfigRef{Path: filepath.Join(dir, "seedfall.json"), Profile: "always"})
	if err != nil {
		t.Fatal(err)
	}
	base := *cfg
	base.Profile = ""
	if p := NewPin(cfg); !p.matches(cfg) || p.matches(&base) {
		t.Errorf("pin %+v should match only the always profile", p)
	}
}

func TestSeedFilesLoadDiskImagesAndFiles(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"image/data/log":       "from image",
		"image/data/keep":      "kept",
		"fixtures/config":      "from files",
		"fixtures/log":         "override",
		"seedfall.json":        `{"nodes": [{"name": "a", "pkg": "./server", "disk": "image", "files": {"/etc/app.conf": "fixtures/config", "data/log": "fixtures/log"}}], "workload": {"name": "client", "pkg": "./client"}}`,
		"image/empty/.gitkeep": "",
	})
	cfg, err := LoadConfig(filepath.Join(dir, "seedfall.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range cfg.disks["a"] {
		if !f.dir {
			got[f.path] = string(f.data)
		}
	}
	want := map[string]string{"data/log": "override", "data/keep": "kept", "etc/app.conf": "from files", "empty/.gitkeep": ""}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("%s = %q, want %q (all: %v)", p, got[p], w, got)
		}
	}
	digest := cfg.Digest()
	if err := os.WriteFile(filepath.Join(dir, "fixtures", "config"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := LoadConfig(filepath.Join(dir, "seedfall.json"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Digest() == digest {
		t.Error("changing a fixture did not change the config digest")
	}
}

func TestSeedFilesRejectMissingHostFiles(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"seedfall.json": `{"nodes": [{"name": "a", "pkg": "./server", "files": {"x": "missing"}}], "workload": {"name": "client", "pkg": "./client"}}`,
	})
	if _, err := LoadConfig(filepath.Join(dir, "seedfall.json")); err == nil || !strings.Contains(err.Error(), `node "a"`) {
		t.Fatalf("err = %v, want a missing fixture error naming the node", err)
	}
}

func TestSeedDiskMustBeADirectory(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"image":         "a file, not a directory",
		"seedfall.json": `{"nodes": [{"name": "a", "pkg": "./server", "disk": "image"}], "workload": {"name": "client", "pkg": "./client"}}`,
	})
	if _, err := LoadConfig(filepath.Join(dir, "seedfall.json")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err = %v, want a disk that is not a directory rejected", err)
	}
}

func TestOverridesGetTheirOwnPinsAndArtifacts(t *testing.T) {
	dir := writeFiles(t, map[string]string{"seedfall.json": baseConfig})
	path := filepath.Join(dir, "seedfall.json")
	base, err := Load(ConfigRef{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	set, err := Load(ConfigRef{Path: path, Sets: []string{"duration=10s"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := Load(ConfigRef{Path: path, Sets: []string{"duration=20s"}})
	if err != nil {
		t.Fatal(err)
	}
	if PinPath(base, 3) == PinPath(set, 3) || PinPath(set, 3) == PinPath(other, 3) || ArtifactDir(base, 3, "") == ArtifactDir(set, 3, "") {
		t.Errorf("overrides share files: %s, %s, %s", PinPath(base, 3), PinPath(set, 3), PinPath(other, 3))
	}
}

func TestSeededFilesAreDurableAcrossCrashes(t *testing.T) {
	for _, strict := range []bool{false, true} {
		m := testFS()
		if strict {
			m.strict()
		}
		files, size := m.seed([]seedFile{{path: "data/db", data: []byte("v1")}, {path: "empty", dir: true}})
		if files != 1 || size != 2 {
			t.Fatalf("seed = %d files, %d bytes", files, size)
		}
		for seed := range uint64(20) {
			m.crash(seeded(seed), crashModel{torn: true, pages: seeded(seed), corrupt: seeded(seed)})
			if got, ok := contents(m, "data/db"); !ok || got != "v1" {
				t.Fatalf("strict %t seed %d: data/db = %q, %t after a crash", strict, seed, got, ok)
			}
			if !m.exists("empty") {
				t.Fatalf("strict %t: seeded directory lost in a crash", strict)
			}
		}
	}
}

func TestGuestStartsFromASeededDiskAndUpgrades(t *testing.T) {
	wasm := guestWasm(t)
	dir := writeFiles(t, map[string]string{"fixture/log": "seeded record\n"})
	cfg := defaultConfig()
	cfg.Nodes = []NodeSpec{{Name: "store", Wasm: wasm, Args: []string{"-role", "store"}, Disk: "fixture", UpgradeTo: "./v2", upgradeWasm: wasm}}
	cfg.Workload = NodeSpec{Name: "client", Wasm: wasm, Args: []string{"-role", "client"}}
	cfg.Duration = Duration(5 * time.Second)
	cfg.Faults.CrashPerSec = 2
	cfg.Faults.RestartAfter = Range{Duration(10 * time.Millisecond), Duration(100 * time.Millisecond)}
	cfg.Dir = dir
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.loadDisks(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.rt.Close(context.Background()) })
	in, err := r.Inspect(1, Schedule{}, InspectOptions{At: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	var log *FileState
	for _, d := range in.Disks {
		if d.Node == "store" {
			for i := range d.Files {
				if d.Files[i].Path == "log" {
					log = &d.Files[i]
				}
			}
		}
	}
	if log == nil || string(log.Durable) != "seeded record\n" {
		t.Fatalf("store's log at t=1ns = %+v, want the seeded record, durable", log)
	}
	res := r.Run(1, nil)
	trace := strings.Join(res.Trace, "\n")
	if !strings.Contains(trace, "store's disk starts with 1 durable files, 14 bytes") {
		t.Errorf("trace does not report the seeded disk:\n%s", trace)
	}
	if !strings.Contains(trace, "start store#2, upgraded to ./v2") {
		t.Errorf("trace does not report the upgrade on the first restart:\n%s", trace)
	}
}
