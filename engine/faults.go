package engine

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

const (
	FaultCrash       = "crash"
	FaultPartition   = "partition"
	FaultCrashIO     = "crash_io"
	FaultFsyncEIO    = "fsync_eio"
	FaultPause       = "pause"
	FaultClockJump   = "clock_jump"
	FaultBuggify     = "buggify"
	FaultClog        = "clog"
	FaultReset       = "net_reset"
	FaultSlow        = "slow"
	FaultWriteEIO    = "write_eio"
	FaultENOSPC      = "write_enospc"
	FaultReadEIO     = "read_eio"
	FaultReadCorrupt = "read_corrupt"
	FaultLostWrite   = "lost_write"
	FaultMisdirected = "misdirected_write"
	FaultBitrot      = "bitrot"
	FaultPreempt     = "preempt"
	FaultUDPDrop     = "udp_drop"
	FaultUDPDup      = "udp_dup"
	DamageCrash      = "corrupt_on_crash"
)

type FaultPreset struct {
	Name  string
	Adds  string
	apply func(*FaultSpec)
}

var FaultPresets = []FaultPreset{
	{"none", "no faults", func(*FaultSpec) {}},
	{"durability", "crashes with restarts, crashes in the middle of disk operations, torn and reordered writes, strict directory durability", func(s *FaultSpec) {
		s.CrashPerSec, s.CrashOnIO, s.TornWrites, s.ReorderWrites, s.StrictDirs = 0.5, 0.002, true, true, true
	}},
	{"resilient", "process pauses, slow nodes, connection resets and wall clock jumps", func(s *FaultSpec) {
		s.PausePerSec, s.SlowPerSec, s.ResetPerSec, s.ClockJumpPerSec = 0.1, 0.1, 0.1, 0.05
	}},
	{"high_availability", "partitions, clogged links, swizzles and lost or duplicated UDP datagrams", func(s *FaultSpec) {
		s.PartitionPerSec, s.ClogPerSec, s.SwizzlePerSec, s.UDPLoss, s.UDPDup = 0.2, 0.2, 0.05, 0.01, 0.01
	}},
	{"everything", "fsync, write and read errors, full disks, corrupted, lost and misdirected writes, crash corruption, bit rot and buggify", func(s *FaultSpec) {
		s.FsyncEIO, s.WriteEIO, s.WriteENOSPC, s.ReadEIO = 0.01, 0.005, 0.005, 0.005
		s.ReadCorrupt, s.LostWrite, s.MisdirectedWrite, s.CorruptOnCrash, s.BitrotPerSec, s.Buggify = 0.005, 0.005, 0.005, true, 0.05, true
	}},
}

func presetFaults(base FaultSpec, name string) (FaultSpec, error) {
	i := slices.IndexFunc(FaultPresets, func(p FaultPreset) bool { return p.Name == name })
	if i < 0 {
		return base, fmt.Errorf("config: unknown faults preset %q, want one of %s", name, strings.Join(PresetNames(), ", "))
	}
	for _, p := range FaultPresets[:i+1] {
		p.apply(&base)
	}
	base.Preset = name
	return base, nil
}

func PresetNames() []string {
	var names []string
	for _, p := range FaultPresets {
		names = append(names, p.Name)
	}
	return names
}

var runtimeKinds = []string{FaultCrashIO, FaultFsyncEIO, FaultBuggify, FaultWriteEIO, FaultENOSPC, FaultReadEIO, FaultReadCorrupt, FaultLostWrite, FaultMisdirected, FaultPreempt, FaultUDPDrop, FaultUDPDup}

func observedAtRuntime(kind string) bool {
	return slices.Contains(runtimeKinds, kind)
}

type Fault struct {
	At     Duration   `json:"at"`
	Kind   string     `json:"kind"`
	Node   string     `json:"node,omitempty"`
	Site   string     `json:"site,omitempty"`
	Side   []string   `json:"side,omitempty"`
	Groups [][]string `json:"groups,omitempty"`
	From   string     `json:"from,omitempty"`
	To     string     `json:"to,omitempty"`
	Dir    string     `json:"dir,omitempty"`
	Mode   string     `json:"mode,omitempty"`
	Delay  Duration   `json:"delay,omitempty"`
	Factor float64    `json:"factor,omitempty"`
	Until  Duration   `json:"until"`
	Op     int64      `json:"op,omitempty"`
	Shift  Duration   `json:"shift,omitempty"`
	Path   string     `json:"path,omitempty"`
	Offset int64      `json:"offset,omitempty"`
	Target int64      `json:"target,omitempty"`
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
	s.Buggify = s.Buggify && r.IntN(2) == 0
	s.ClogPerSec = swarmRate(r, s.ClogPerSec)
	s.SwizzlePerSec = swarmRate(r, s.SwizzlePerSec)
	s.ResetPerSec = swarmRate(r, s.ResetPerSec)
	s.SlowPerSec = swarmRate(r, s.SlowPerSec)
	s.WriteEIO = swarmRate(r, s.WriteEIO)
	s.WriteENOSPC = swarmRate(r, s.WriteENOSPC)
	s.ReadEIO = swarmRate(r, s.ReadEIO)
	s.ReadCorrupt = swarmRate(r, s.ReadCorrupt)
	s.LostWrite = swarmRate(r, s.LostWrite)
	s.MisdirectedWrite = swarmRate(r, s.MisdirectedWrite)
	s.CorruptOnCrash = s.CorruptOnCrash && r.IntN(2) == 0
	s.BitrotPerSec = swarmRate(r, s.BitrotPerSec)
	s.UDPLoss = swarmRate(r, s.UDPLoss)
	s.UDPDup = swarmRate(r, s.UDPDup)
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
		for _, n := range faultTargets(cfg) {
			for t := exponential(r, rate); t < horizon; {
				until := t + uniform(r, spec.RestartAfter)
				out = append(out, Fault{At: Duration(t), Kind: FaultCrash, Node: n, Until: Duration(until)})
				t = until + exponential(r, rate)
			}
		}
	}
	if rate := spec.PartitionPerSec; rate > 0 {
		names := nodeNames(cfg)
		for t := exponential(r, rate); t < horizon; {
			until := t + uniform(r, spec.PartitionFor)
			f := randomPartition(r, names, spec.PartitionTopologies)
			f.At, f.Until = Duration(t), Duration(until)
			out = append(out, f)
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
	out = append(out, netFaults(cfg, spec, r)...)
	out = append(out, perNode(cfg, spec.BitrotPerSec, r, func(n string, t time.Duration) (Fault, time.Duration) {
		return Fault{At: Duration(t), Kind: FaultBitrot, Node: n, Until: Duration(t)}, t
	})...)
	out = slices.DeleteFunc(out, func(f Fault) bool { return time.Duration(f.At) >= horizon })
	slices.SortStableFunc(out, func(a, b Fault) int { return int(a.At - b.At) })
	return out
}

func netFaults(cfg *Config, spec FaultSpec, r *rand.Rand) []Fault {
	var out []Fault
	horizon := time.Duration(cfg.Duration)
	names := nodeNames(cfg)
	if rate := spec.ClogPerSec; rate > 0 {
		for t := exponential(r, rate); t < horizon; {
			until := t + uniform(r, spec.ClogFor)
			f := randomClog(r, names, spec)
			f.At, f.Until = Duration(t), Duration(until)
			out = append(out, f)
			t = until + exponential(r, rate)
		}
	}
	if rate := spec.SwizzlePerSec; rate > 0 {
		for t := exponential(r, rate); t < horizon; {
			clogs, end := swizzle(r, t, names, spec)
			out = append(out, clogs...)
			t = end + exponential(r, rate)
		}
	}
	out = append(out, perNode(cfg, spec.ResetPerSec, r, func(n string, t time.Duration) (Fault, time.Duration) {
		return Fault{At: Duration(t), Kind: FaultReset, Node: n, Until: Duration(t)}, t
	})...)
	out = append(out, perNode(cfg, spec.SlowPerSec, r, func(n string, t time.Duration) (Fault, time.Duration) {
		until := t + uniform(r, spec.SlowFor)
		return Fault{At: Duration(t), Kind: FaultSlow, Node: n, Factor: slowFactor(r, spec.SlowFactor), Until: Duration(until)}, until
	})...)
	return out
}

func perNode(cfg *Config, rate float64, r *rand.Rand, fault func(node string, at time.Duration) (Fault, time.Duration)) []Fault {
	if rate <= 0 {
		return nil
	}
	var out []Fault
	horizon := time.Duration(cfg.Duration)
	for _, n := range faultTargets(cfg) {
		for t := exponential(r, rate); t < horizon; {
			f, end := fault(n, t)
			out = append(out, f)
			t = end + exponential(r, rate)
		}
	}
	return out
}

func nodeNames(cfg *Config) []string { return namesWhere(cfg, cfg.networked) }

func faultTargets(cfg *Config) []string { return namesWhere(cfg, cfg.faultable) }

func namesWhere(cfg *Config, keep func(NodeSpec) bool) []string {
	var names []string
	for _, s := range cfg.all() {
		if keep(s) {
			names = append(names, s.Name)
		}
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
	if text := f.diskText(); text != "" {
		return fmt.Sprintf("%8s  %s", at(f.At), text)
	}
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
	case FaultBuggify:
		return fmt.Sprintf("%8s  buggify %q fires on %s at hit #%d", at(f.At), f.Site, f.Node, f.Op)
	case FaultClog:
		return fmt.Sprintf("%8s  clog %s until %s", at(f.At), f.clogTarget(), at(f.Until))
	case FaultReset:
		return fmt.Sprintf("%8s  reset every connection of %s", at(f.At), f.Node)
	case FaultSlow:
		return fmt.Sprintf("%8s  slow %s down %.1fx until %s", at(f.At), f.Node, f.Factor, at(f.Until))
	case FaultPreempt:
		return fmt.Sprintf("%8s  preempt %s's running goroutine at counted call #%d", at(f.At), f.Node, f.Op)
	case FaultUDPDrop, FaultUDPDup:
		return fmt.Sprintf("%8s  %s", at(f.At), f.udpText())
	}
	return fmt.Sprintf("%8s  %s until %s", at(f.At), f.partitionText(), at(f.Until))
}
