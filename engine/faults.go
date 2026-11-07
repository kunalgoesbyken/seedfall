package engine

import (
	"fmt"
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
	FaultPause     = "pause"
	FaultClockJump = "clock_jump"
)

func observedAtRuntime(kind string) bool { return kind == FaultCrashIO || kind == FaultFsyncEIO }

type Fault struct {
	At    Duration `json:"at"`
	Kind  string   `json:"kind"`
	Node  string   `json:"node,omitempty"`
	Side  []string `json:"side,omitempty"`
	Until Duration `json:"until"`
	Op    int64    `json:"op,omitempty"`
	Shift Duration `json:"shift,omitempty"`
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
	s.PausePerSec = swarmRate(r, s.PausePerSec)
	s.ClockJumpPerSec = swarmRate(r, s.ClockJumpPerSec)
	s.TornWrites = s.TornWrites && r.IntN(2) == 0
	s.ReorderWrites = s.ReorderWrites && r.IntN(2) == 0
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
	out = append(out, perNode(cfg, spec.PausePerSec, r, func(n string, t time.Duration) (Fault, time.Duration) {
		until := t + uniform(r, spec.PauseFor)
		return Fault{At: Duration(t), Kind: FaultPause, Node: n, Until: Duration(until)}, until
	})...)
	out = append(out, perNode(cfg, spec.ClockJumpPerSec, r, func(n string, t time.Duration) (Fault, time.Duration) {
		limit := int64(spec.ClockJumpMax)
		shift := r.Int64N(2*limit+1) - limit
		return Fault{At: Duration(t), Kind: FaultClockJump, Node: n, Shift: Duration(shift)}, t
	})...)
	slices.SortStableFunc(out, func(a, b Fault) int { return int(a.At - b.At) })
	return out
}

func perNode(cfg *Config, rate float64, r *rand.Rand, fault func(node string, at time.Duration) (Fault, time.Duration)) []Fault {
	if rate <= 0 {
		return nil
	}
	var out []Fault
	horizon := time.Duration(cfg.Duration)
	for _, n := range cfg.Nodes {
		for t := exponential(r, rate); t < horizon; {
			f, end := fault(n.Name, t)
			out = append(out, f)
			t = end + exponential(r, rate)
		}
	}
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

func (f Fault) Describe() string {
	at := func(d Duration) time.Duration { return time.Duration(d).Round(time.Microsecond) }
	switch f.Kind {
	case FaultFsyncEIO:
		return fmt.Sprintf("%8s  fsync on %s fails with EIO at disk op #%d, unsynced writes dropped", at(f.At), f.Node, f.Op)
	case FaultCrashIO:
		return fmt.Sprintf("%8s  crash %s during disk op #%d, restart at %s", at(f.At), f.Node, f.Op, at(f.Until))
	case FaultCrash:
		return fmt.Sprintf("%8s  crash %s, restart at %s", at(f.At), f.Node, at(f.Until))
	case FaultPause:
		return fmt.Sprintf("%8s  pause %s until %s", at(f.At), f.Node, at(f.Until))
	case FaultClockJump:
		return fmt.Sprintf("%8s  %s's wall clock jumps %s", at(f.At), f.Node, fmtSigned(int64(f.Shift)))
	}
	return fmt.Sprintf("%8s  partition %v from the rest until %s", at(f.At), f.Side, at(f.Until))
}
