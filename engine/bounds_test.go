package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validConfig() *Config {
	cfg := defaultConfig()
	cfg.Nodes = []NodeSpec{{Name: "n", Pkg: "./n"}}
	cfg.Workload = NodeSpec{Name: "w", Pkg: "./w"}
	return &cfg
}

func TestConfigBoundsAreChecked(t *testing.T) {
	second := Duration(time.Second)
	for _, c := range []struct {
		name   string
		edit   func(*Config)
		reason string
	}{
		{"crash_on_io above 1", func(c *Config) { c.Faults.CrashOnIO = 1.5 }, "faults.crash_on_io must be between 0 and 1"},
		{"fsync_eio negative", func(c *Config) { c.Faults.FsyncEIO = -0.1 }, "faults.fsync_eio must be between 0 and 1"},
		{"write_eio above 1", func(c *Config) { c.Faults.WriteEIO = 2 }, "faults.write_eio"},
		{"write_enospc above 1", func(c *Config) { c.Faults.WriteENOSPC = 2 }, "faults.write_enospc"},
		{"read_eio above 1", func(c *Config) { c.Faults.ReadEIO = 2 }, "faults.read_eio"},
		{"read_corrupt above 1", func(c *Config) { c.Faults.ReadCorrupt = 2 }, "faults.read_corrupt"},
		{"lost_write above 1", func(c *Config) { c.Faults.LostWrite = 2 }, "faults.lost_write"},
		{"misdirected_write above 1", func(c *Config) { c.Faults.MisdirectedWrite = 2 }, "faults.misdirected_write"},
		{"mmap_early_writeback above 1", func(c *Config) { c.Faults.MmapEarlyWriteback = 2 }, "faults.mmap_early_writeback"},
		{"udp_loss above 1", func(c *Config) { c.Faults.UDPLoss = 2 }, "faults.udp_loss must be between 0 and 1"},
		{"udp_dup negative", func(c *Config) { c.Faults.UDPDup = -1 }, "faults.udp_dup"},
		{"crash_per_sec negative", func(c *Config) { c.Faults.CrashPerSec = -1 }, "faults.crash_per_sec must not be negative"},
		{"bitrot_per_sec negative", func(c *Config) { c.Faults.BitrotPerSec = -1 }, "faults.bitrot_per_sec"},
		{"clock_jump_per_sec negative", func(c *Config) { c.Faults.ClockJumpPerSec = -1 }, "faults.clock_jump_per_sec"},
		{"latency reversed", func(c *Config) { c.Latency = Range{second, 0} }, "latency must be [min, max]"},
		{"latency negative", func(c *Config) { c.Latency = Range{-second, second} }, "latency must be [min, max]"},
		{"link latency reversed", func(c *Config) { c.Links = []Link{{Latency: Range{second, 0}}} }, "links[0].latency"},
		{"restart_after reversed", func(c *Config) { c.Faults.RestartAfter = Range{second, 0} }, "faults.restart_after"},
		{"partition_for reversed", func(c *Config) { c.Faults.PartitionFor = Range{second, 0} }, "faults.partition_for"},
		{"clog_for reversed", func(c *Config) { c.Faults.ClogFor = Range{second, 0} }, "faults.clog_for"},
		{"clog_delay reversed", func(c *Config) { c.Faults.ClogDelay = Range{second, 0} }, "faults.clog_delay"},
		{"slow_for reversed", func(c *Config) { c.Faults.SlowFor = Range{second, 0} }, "faults.slow_for"},
		{"pause_for reversed", func(c *Config) { c.Faults.PauseFor = Range{second, 0} }, "faults.pause_for"},
		{"disk_latency reversed", func(c *Config) { c.Faults.DiskLatency = Range{second, 0} }, "faults.disk_latency"},
		{"zero watchdog", func(c *Config) { c.Watchdog = 0 }, "watchdog must be positive"},
		{"negative watchdog", func(c *Config) { c.Watchdog = -second }, "watchdog must be positive"},
		{"zero duration", func(c *Config) { c.Duration = 0 }, "duration must be positive"},
		{"negative clock_jump_max", func(c *Config) { c.Faults.ClockJumpMax = -second }, "faults.clock_jump_max must not be negative"},
		{"pkg and wasm", func(c *Config) { c.Workload.Wasm = "w.wasm" }, `node "w" sets both pkg and wasm`},
	} {
		cfg := validConfig()
		c.edit(cfg)
		if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.reason)
		}
	}
	cfg := validConfig()
	cfg.Faults.CrashOnIO, cfg.Faults.UDPLoss, cfg.Faults.CrashPerSec = 1, 0, 0
	cfg.Latency = Range{second, second}
	cfg.Links = []Link{{Latency: Range{}}}
	if err := cfg.validate(); err != nil {
		t.Errorf("the bounds themselves are valid: %v", err)
	}
}

func TestPresetsAreWithinBounds(t *testing.T) {
	for _, name := range PresetNames() {
		cfg := validConfig()
		var err error
		if cfg.Faults, err = presetFaults(cfg.Faults, name); err != nil {
			t.Fatal(err)
		}
		if err := cfg.validate(); err != nil {
			t.Errorf("preset %s: %v", name, err)
		}
	}
}

func TestLoadRejectsUnknownKeysAndReturnsNoConfig(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"typo.json":    `{"workload": {"name": "c", "pkg": "./c"}, "faults": {"crash_per_sc": 1}}`,
		"nested.json":  `{"workload": {"name": "c", "pkg": "./c", "arg": []}}`,
		"top.json":     `{"workload": {"name": "c", "pkg": "./c"}, "durration": "1s"}`,
		"bounds.json":  `{"workload": {"name": "c", "pkg": "./c"}, "faults": {"crash_on_io": 2}}`,
		"profile.json": `{"workload": {"name": "c", "pkg": "./c"}, "profiles": {"x": {"watchdog": "1s", "wachdog": "2s"}}}`,
	})
	for file, want := range map[string]string{
		"typo.json": "crash_per_sc", "nested.json": `"arg"`, "top.json": "durration", "bounds.json": "crash_on_io",
	} {
		cfg, err := LoadConfig(filepath.Join(dir, file))
		if err == nil || !strings.Contains(err.Error(), want) || cfg != nil {
			t.Errorf("%s: config %v, error %v, want no config and an error naming %s", file, cfg, err, want)
		}
	}
	if cfg, err := Load(ConfigRef{Path: filepath.Join(dir, "profile.json"), Profile: "x"}); err == nil || !strings.Contains(err.Error(), "wachdog") || cfg != nil {
		t.Errorf("a typo inside a profile: config %v, error %v", cfg, err)
	}
	if cfg, err := Load(ConfigRef{Path: filepath.Join(dir, "profile.json"), Sets: []string{"faults.crash_per_sc=1"}}); err == nil || cfg != nil {
		t.Errorf("a typo in -set: config %v, error %v", cfg, err)
	}
}
