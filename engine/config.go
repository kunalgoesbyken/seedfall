package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

type Range [2]Duration

type NodeSpec struct {
	Name   string            `json:"name"`
	Pkg    string            `json:"pkg,omitempty"`
	Wasm   string            `json:"wasm,omitempty"`
	Args   []string          `json:"args,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
	Faults *bool             `json:"faults,omitempty"`
}

type FaultSpec struct {
	CrashPerSec         float64  `json:"crash_per_sec"`
	RestartAfter        Range    `json:"restart_after"`
	PartitionPerSec     float64  `json:"partition_per_sec"`
	PartitionFor        Range    `json:"partition_for"`
	PartitionTopologies []string `json:"partition_topologies"`
	ClogPerSec          float64  `json:"clog_per_sec"`
	ClogFor             Range    `json:"clog_for"`
	ClogModes           []string `json:"clog_modes"`
	ClogDelay           Range    `json:"clog_delay"`
	SwizzlePerSec       float64  `json:"swizzle_per_sec"`
	ResetPerSec         float64  `json:"reset_per_sec"`
	SlowPerSec          float64  `json:"slow_per_sec"`
	SlowFor             Range    `json:"slow_for"`
	SlowFactor          float64  `json:"slow_factor"`
	TornWrites          bool     `json:"torn_writes"`
	ReorderWrites       bool     `json:"reorder_writes"`
	CrashOnIO           float64  `json:"crash_on_io"`
	StrictDirs          bool     `json:"strict_dirs"`
	FsyncEIO            float64  `json:"fsync_eio"`
	ClockSkew           Duration `json:"clock_skew"`
	Swarm               bool     `json:"swarm"`
	PausePerSec         float64  `json:"pause_per_sec"`
	PauseFor            Range    `json:"pause_for"`
	ClockJumpPerSec     float64  `json:"clock_jump_per_sec"`
	ClockJumpMax        Duration `json:"clock_jump_max"`
	Buggify             bool     `json:"buggify"`
	DiskBytes           int64    `json:"disk_bytes"`
	WriteEIO            float64  `json:"write_eio"`
	WriteENOSPC         float64  `json:"write_enospc"`
	ReadEIO             float64  `json:"read_eio"`
	ReadCorrupt         float64  `json:"read_corrupt"`
	LostWrite           float64  `json:"lost_write"`
	MisdirectedWrite    float64  `json:"misdirected_write"`
	CorruptOnCrash      bool     `json:"corrupt_on_crash"`
	BitrotPerSec        float64  `json:"bitrot_per_sec"`
	DiskLatency         Range    `json:"disk_latency"`
	IncludeWorkload     bool     `json:"include_workload"`
}

type Link struct {
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Latency Range  `json:"latency"`
}

type PreemptSpec struct {
	Interleave bool `json:"interleave"`
}

type CheckerSpec struct {
	Model string `json:"model" jsonschema:"model the sim.Invoke and sim.Txn history is checked against"`
}

type LivenessSpec struct {
	Within Duration `json:"within"`
}

type Config struct {
	Nodes            []NodeSpec   `json:"nodes"`
	Workload         NodeSpec     `json:"workload"`
	Workloads        []NodeSpec   `json:"workloads,omitempty"`
	Finally          *NodeSpec    `json:"finally,omitempty"`
	Liveness         LivenessSpec `json:"liveness"`
	Checker          CheckerSpec  `json:"checker"`
	Duration         Duration     `json:"duration"`
	Latency          Range        `json:"latency"`
	Links            []Link       `json:"links,omitempty"`
	Faults           FaultSpec    `json:"faults"`
	Watchdog         Duration     `json:"watchdog"`
	NetOverlay       bool         `json:"net_overlay"`
	CompatOverlay    bool         `json:"compat_overlay"`
	Supervise        bool         `json:"supervise"`
	FaultsAfterSetup bool         `json:"faults_after_setup"`
	Preempt          PreemptSpec  `json:"preempt"`
	Dir              string       `json:"-"`
	Path             string       `json:"-"`
}

func defaultConfig() Config {
	return Config{
		Duration: Duration(30 * time.Second),
		Latency:  Range{Duration(time.Millisecond), Duration(10 * time.Millisecond)},
		Watchdog: Duration(30 * time.Second),
		Checker:  CheckerSpec{Model: defaultModel},
		Faults: FaultSpec{
			RestartAfter:        Range{Duration(10 * time.Millisecond), Duration(time.Second)},
			PartitionFor:        Range{Duration(50 * time.Millisecond), Duration(2 * time.Second)},
			PartitionTopologies: []string{TopologyHalf},
			ClogFor:             Range{Duration(100 * time.Millisecond), Duration(2 * time.Second)},
			ClogModes:           slices.Clone(clogModes),
			ClogDelay:           Range{Duration(10 * time.Millisecond), Duration(200 * time.Millisecond)},
			SlowFor:             Range{Duration(500 * time.Millisecond), Duration(5 * time.Second)},
			SlowFactor:          10,
			PauseFor:            Range{Duration(100 * time.Millisecond), Duration(3 * time.Second)},
			ClockJumpMax:        Duration(10 * time.Second),
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Dir, cfg.Path = filepath.Dir(path), path
	return &cfg, cfg.validate()
}

func (c *Config) validate() error {
	if len(c.workloads()) == 0 {
		return errors.New("config: workload.name is required")
	}
	if c.Liveness.Within < 0 {
		return errors.New("config: liveness.within is negative")
	}
	seen := map[string]bool{}
	for _, s := range c.all() {
		if s.Name == "" {
			return errors.New("config: every node needs a name")
		}
		if seen[s.Name] {
			return fmt.Errorf("config: duplicate node name %q", s.Name)
		}
		seen[s.Name] = true
		if strings.HasPrefix(s.Pkg, "-") {
			return fmt.Errorf("config: node %q has an invalid pkg %q", s.Name, s.Pkg)
		}
		if s.Pkg == "" && s.Wasm == "" {
			return fmt.Errorf("config: node %q needs pkg or wasm", s.Name)
		}
	}
	if c.Faults.DiskBytes < 0 {
		return errors.New("config: faults.disk_bytes is negative")
	}
	if err := knownModel(c.Checker.Model); err != nil {
		return fmt.Errorf("config: checker.model: %w", err)
	}
	return c.validateNetwork(seen)
}

func (c *Config) specs() []*NodeSpec {
	var out []*NodeSpec
	for i := range c.Nodes {
		out = append(out, &c.Nodes[i])
	}
	if c.Workload.Name != "" || len(c.Workloads) == 0 {
		out = append(out, &c.Workload)
	}
	for i := range c.Workloads {
		out = append(out, &c.Workloads[i])
	}
	if c.Finally != nil {
		out = append(out, c.Finally)
	}
	return out
}

func (c *Config) all() []NodeSpec {
	var out []NodeSpec
	for _, s := range c.specs() {
		out = append(out, *s)
	}
	return out
}

func (c *Config) workloads() []NodeSpec {
	return slices.DeleteFunc(c.all(), func(s NodeSpec) bool { return s.Name == "" || !c.isWorkload(s.Name) })
}

func (c *Config) isWorkload(name string) bool {
	return name == c.Workload.Name || slices.ContainsFunc(c.Workloads, func(s NodeSpec) bool { return s.Name == name })
}

func (c *Config) isFinally(name string) bool { return c.Finally != nil && c.Finally.Name == name }

func (c *Config) networked(s NodeSpec) bool {
	return (s.Faults == nil || *s.Faults) && !c.isFinally(s.Name)
}

func (c *Config) faultable(s NodeSpec) bool {
	switch {
	case c.isFinally(s.Name):
		return false
	case s.Faults != nil:
		return *s.Faults
	}
	return !c.isWorkload(s.Name) || c.Faults.IncludeWorkload
}

func DefaultConfig() Config { return defaultConfig() }
