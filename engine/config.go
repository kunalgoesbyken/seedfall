package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Name string            `json:"name"`
	Pkg  string            `json:"pkg,omitempty"`
	Wasm string            `json:"wasm,omitempty"`
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

type FaultSpec struct {
	CrashPerSec     float64 `json:"crash_per_sec"`
	RestartAfter    Range   `json:"restart_after"`
	PartitionPerSec float64 `json:"partition_per_sec"`
	PartitionFor    Range   `json:"partition_for"`
	TornWrites      bool    `json:"torn_writes"`
	CrashOnIO       float64 `json:"crash_on_io"`
	StrictDirs      bool    `json:"strict_dirs"`
}

type Config struct {
	Nodes    []NodeSpec `json:"nodes"`
	Workload NodeSpec   `json:"workload"`
	Duration Duration   `json:"duration"`
	Latency  Range      `json:"latency"`
	Faults   FaultSpec  `json:"faults"`
	Watchdog Duration   `json:"watchdog"`
	Dir      string     `json:"-"`
}

func defaultConfig() Config {
	return Config{
		Duration: Duration(30 * time.Second),
		Latency:  Range{Duration(time.Millisecond), Duration(10 * time.Millisecond)},
		Watchdog: Duration(30 * time.Second),
		Faults: FaultSpec{
			RestartAfter: Range{Duration(10 * time.Millisecond), Duration(time.Second)},
			PartitionFor: Range{Duration(50 * time.Millisecond), Duration(2 * time.Second)},
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
	cfg.Dir = filepath.Dir(path)
	return &cfg, cfg.validate()
}

func (c *Config) validate() error {
	if c.Workload.Name == "" {
		return errors.New("config: workload.name is required")
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
		if s.Pkg == "" && s.Wasm == "" {
			return fmt.Errorf("config: node %q needs pkg or wasm", s.Name)
		}
	}
	return nil
}

func (c *Config) all() []NodeSpec { return append(append([]NodeSpec{}, c.Nodes...), c.Workload) }
