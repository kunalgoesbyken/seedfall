package engine

import (
	"encoding/json"
	"errors"
	"fmt"
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
	Name      string            `json:"name"`
	Pkg       string            `json:"pkg,omitempty"`
	Wasm      string            `json:"wasm,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Faults    *bool             `json:"faults,omitempty"`
	Files     map[string]string `json:"files,omitempty" jsonschema:"files placed on the node's disk before it first starts, durable and synced: guest path to host path, relative to the config's directory"`
	Disk      string            `json:"disk,omitempty" jsonschema:"host directory copied onto the node's disk before it first starts, durable and synced, for example a node directory written by seedfall inspect -out"`
	UpgradeTo string            `json:"upgrade_to,omitempty" jsonschema:"Go package that replaces pkg from the node's first restart on, to test upgrades against the data the old version left on disk"`

	upgradeWasm string
}

type FaultSpec struct {
	Preset              string   `json:"preset,omitempty" jsonschema:"start from a built-in fault level: none, durability, resilient, high_availability or everything; fields set next to it override it"`
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
	Interleave bool   `json:"interleave" jsonschema:"shorthand for strategy random"`
	Strategy   string `json:"strategy" jsonschema:"how the running goroutine is preempted: none (only after a full time slice), random (after a seeded random count of up to 4096 calls), pct (at depth-1 seeded change points per node) or calls (every calls counted calls)"`
	Depth      int    `json:"depth" jsonschema:"pct: the bug depth d, giving d-1 change points per node"`
	Steps      int64  `json:"steps" jsonschema:"pct: counted calls per node that the change points are drawn over; the trace's pct line shows how many each node ran"`
	Calls      int64  `json:"calls" jsonschema:"calls: counted calls between preemptions"`
	Loops      bool   `json:"loops" jsonschema:"count loop iterations as well as function calls, so a loop without calls is preempted and uses virtual time"`
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
	Profile          string       `json:"-"`
	Sets             []string     `json:"-"`

	resolved []byte
	disks    map[string][]seedFile
}

func defaultConfig() Config {
	return Config{
		Duration: Duration(30 * time.Second),
		Latency:  Range{Duration(time.Millisecond), Duration(10 * time.Millisecond)},
		Watchdog: Duration(30 * time.Second),
		Checker:  CheckerSpec{Model: defaultModel},
		Preempt:  PreemptSpec{Strategy: StrategyNone, Depth: 3, Steps: pctSteps, Calls: interleaveCalls},
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

func (c *Config) Stem() string {
	stem := strings.TrimSuffix(filepath.Base(c.Path), filepath.Ext(c.Path))
	if c.Profile != "" {
		stem += "." + c.Profile
	}
	if len(c.Sets) > 0 {
		stem += ".set-" + setsDigest(c.Sets)
	}
	return stem
}

func (c *Config) Ref() ConfigRef { return ConfigRef{Path: c.Path, Profile: c.Profile, Sets: c.Sets} }

func LoadConfig(path string) (*Config, error) { return Load(ConfigRef{Path: path}) }

func Load(ref ConfigRef) (*Config, error) {
	doc, err := resolveLayers(ref)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if faults, ok := doc["faults"].(map[string]any); ok && faults["preset"] != nil {
		name, _ := faults["preset"].(string)
		if cfg.Faults, err = presetFaults(cfg.Faults, name); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	cfg.Dir, cfg.Path, cfg.Profile, cfg.Sets, cfg.resolved = filepath.Dir(ref.Path), ref.Path, ref.Profile, ref.Sets, b
	if err := cfg.validate(); err != nil {
		return &cfg, err
	}
	return &cfg, cfg.loadDisks()
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
		if strings.HasPrefix(s.UpgradeTo, "-") {
			return fmt.Errorf("config: node %q has an invalid upgrade_to %q", s.Name, s.UpgradeTo)
		}
	}
	if c.Faults.DiskBytes < 0 {
		return errors.New("config: faults.disk_bytes is negative")
	}
	if err := knownModel(c.Checker.Model); err != nil {
		return fmt.Errorf("config: checker.model: %w", err)
	}
	if err := c.Preempt.normalize(); err != nil {
		return fmt.Errorf("config: preempt: %w", err)
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
