package engine

import (
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

const (
	FaultCrash     = "crash"
	FaultPartition = "partition"
	FaultCrashIO   = "crash_io"
	FaultFsyncEIO  = "fsync_eio"
)

func observedAtRuntime(kind string) bool { return kind == FaultCrashIO || kind == FaultFsyncEIO }

type Fault struct {
	At    Duration `json:"at"`
	Kind  string   `json:"kind"`
	Node  string   `json:"node,omitempty"`
	Side  []string `json:"side,omitempty"`
	Until Duration `json:"until"`
	Op    int64    `json:"op,omitempty"`
}

func uniform(r *rand.Rand, rg Range) time.Duration {
	lo, hi := int64(rg[0]), int64(rg[1])
	if hi <= lo {
		return time.Duration(lo)
	}
	return time.Duration(lo + r.Int64N(hi-lo+1))
}

func exponential(r *rand.Rand, perSec float64) time.Duration {
	return time.Duration(-math.Log(1-r.Float64()) / perSec * float64(time.Second))
}

func (s FaultSpec) forSeed(seed uint64) FaultSpec {
	if !s.Swarm {
		return s
	}
	r := streams(seed, "swarm")
	s.CrashPerSec = swarmRate(r, s.CrashPerSec)
	s.PartitionPerSec = swarmRate(r, s.PartitionPerSec)
	s.CrashOnIO = swarmRate(r, s.CrashOnIO)
	s.FsyncEIO = swarmRate(r, s.FsyncEIO)
	s.TornWrites = s.TornWrites && r.IntN(2) == 0
	return s
}

func swarmRate(r *rand.Rand, rate float64) float64 {
	if r.IntN(2) == 0 {
		return 0
	}
	return rate * 2 * r.Float64()
}

func generateFaults(cfg *Config, spec FaultSpec, r *rand.Rand) []Fault {
	var out []Fault
	horizon := time.Duration(cfg.Duration)
	if rate := spec.CrashPerSec; rate > 0 {
		for _, n := range cfg.Nodes {
			for t := exponential(r, rate); t < horizon; {
				until := t + uniform(r, spec.RestartAfter)
				out = append(out, Fault{At: Duration(t), Kind: FaultCrash, Node: n.Name, Until: Duration(until)})
				t = until + exponential(r, rate)
			}
		}
	}
	if rate := spec.PartitionPerSec; rate > 0 {
		names := nodeNames(cfg)
		for t := exponential(r, rate); t < horizon; {
			until := t + uniform(r, spec.PartitionFor)
			out = append(out, Fault{At: Duration(t), Kind: FaultPartition, Side: randomSide(r, names), Until: Duration(until)})
			t = until + exponential(r, rate)
		}
	}
	slices.SortStableFunc(out, func(a, b Fault) int { return int(a.At - b.At) })
	return out
}

func nodeNames(cfg *Config) []string {
	var names []string
	for _, s := range cfg.all() {
		names = append(names, s.Name)
	}
	return names
}

func randomSide(r *rand.Rand, names []string) []string {
	for {
		var side []string
		for _, n := range names {
			if r.IntN(2) == 0 {
				side = append(side, n)
			}
		}
		if len(side) > 0 && len(side) < len(names) {
			return side
		}
	}
}
