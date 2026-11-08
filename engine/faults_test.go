package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func faultConfig() *Config {
	cfg := defaultConfig()
	cfg.Nodes = []NodeSpec{{Name: "n1"}, {Name: "n2"}, {Name: "n3"}}
	cfg.Workload = NodeSpec{Name: "client"}
	cfg.Duration = Duration(20 * time.Second)
	cfg.Faults.CrashPerSec = 0.5
	cfg.Faults.PartitionPerSec = 0.3
	cfg.Faults.PausePerSec = 0.2
	cfg.Faults.ClockJumpPerSec = 0.2
	cfg.Faults.CrashOnIO = 0.01
	cfg.Faults.FsyncEIO = 0.01
	cfg.Faults.TornWrites = true
	cfg.Faults.ReorderWrites = true
	cfg.Faults.BitrotPerSec = 0.2
	cfg.Faults.ReadCorrupt = 0.01
	cfg.Faults.LostWrite = 0.01
	return &cfg
}

func faultsFor(cfg *Config, seed uint64) []Fault {
	return generateFaults(cfg, cfg.Faults.forSeed(seed), streams(seed, "faults"))
}

func TestGenerateFaultsIsDeterministicPerSeed(t *testing.T) {
	cfg := faultConfig()
	distinct := 0
	for seed := range uint64(50) {
		a, b := faultsFor(cfg, seed), faultsFor(cfg, seed)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("seed %d: two schedules differ:\n%v\n%v", seed, a, b)
		}
		if !reflect.DeepEqual(a, faultsFor(cfg, seed+1)) {
			distinct++
		}
	}
	if distinct == 0 {
		t.Fatal("every seed produced the same schedule")
	}
}

func TestGenerateFaultsRespectsTheHorizonAndOrder(t *testing.T) {
	cfg := faultConfig()
	horizon := cfg.Duration
	kinds := map[string]bool{}
	for seed := range uint64(50) {
		faults := faultsFor(cfg, seed)
		if !slices.IsSortedFunc(faults, func(a, b Fault) int { return int(a.At - b.At) }) {
			t.Fatalf("seed %d: faults out of order: %v", seed, faults)
		}
		for _, f := range faults {
			kinds[f.Kind] = true
			if f.At < 0 || f.At >= horizon {
				t.Fatalf("seed %d: %s at %s is outside [0, %s)", seed, f.Kind, time.Duration(f.At), time.Duration(horizon))
			}
			if f.Kind != FaultClockJump && f.Until < f.At {
				t.Fatalf("seed %d: %s ends at %s before it starts at %s", seed, f.Kind, time.Duration(f.Until), time.Duration(f.At))
			}
			if f.Kind == FaultClockJump && (f.Shift < -cfg.Faults.ClockJumpMax || f.Shift > cfg.Faults.ClockJumpMax) {
				t.Fatalf("seed %d: clock jump of %s exceeds %s", seed, time.Duration(f.Shift), time.Duration(cfg.Faults.ClockJumpMax))
			}
			if f.Kind == FaultCrash && f.Node == cfg.Workload.Name {
				t.Fatalf("seed %d: the workload node was scheduled to crash", seed)
			}
		}
	}
	for _, k := range []string{FaultCrash, FaultPartition, FaultPause, FaultClockJump, FaultBitrot} {
		if !kinds[k] {
			t.Errorf("no seed scheduled a %s", k)
		}
	}
	for _, k := range runtimeKinds {
		if kinds[k] {
			t.Errorf("%s is observed at runtime and must not be planned", k)
		}
	}
}

func TestGenerateFaultsWithZeroRatesIsEmpty(t *testing.T) {
	cfg := faultConfig()
	cfg.Faults = defaultConfig().Faults
	if got := faultsFor(cfg, 1); len(got) != 0 {
		t.Fatalf("no rates set but scheduled %v", got)
	}
}

func TestSwarmRateStaysWithinBounds(t *testing.T) {
	r := seeded(3)
	const rate = 0.4
	off, on := 0, 0
	for range 2000 {
		got := swarmRate(r, rate)
		if got < 0 || got >= 2*rate {
			t.Fatalf("swarmRate(%v) = %v, want 0 or within [0, %v)", rate, got, 2*rate)
		}
		if got == 0 {
			off++
		} else {
			on++
		}
	}
	if off < 800 || on < 800 {
		t.Fatalf("swarm turned the fault off %d times and on %d times out of 2000, want about half each", off, on)
	}
}

func TestForSeedVariesOnlyUnderSwarm(t *testing.T) {
	spec := faultConfig().Faults
	if got := spec.forSeed(9); !reflect.DeepEqual(got, spec) {
		t.Fatalf("forSeed changed a non-swarm spec: %+v", got)
	}
	spec.Swarm = true
	mixes := map[string]bool{}
	for seed := range uint64(40) {
		a := spec.forSeed(seed)
		if !reflect.DeepEqual(a, spec.forSeed(seed)) {
			t.Fatalf("seed %d: swarm mix is not deterministic", seed)
		}
		if a.CrashPerSec > 2*spec.CrashPerSec || a.PartitionPerSec > 2*spec.PartitionPerSec {
			t.Fatalf("seed %d: swarm raised a rate beyond twice the configured one: %+v", seed, a)
		}
		if a.StrictDirs != spec.StrictDirs || a.RestartAfter != spec.RestartAfter || !slices.Equal(a.ClogModes, spec.ClogModes) {
			t.Fatalf("seed %d: swarm changed a setting it does not own: %+v", seed, a)
		}
		if a.ClogPerSec > 2*spec.ClogPerSec || a.SlowPerSec > 2*spec.SlowPerSec || a.ResetPerSec > 2*spec.ResetPerSec || a.SwizzlePerSec > 2*spec.SwizzlePerSec {
			t.Fatalf("seed %d: swarm raised a network rate beyond twice the configured one: %+v", seed, a)
		}
		if a.BitrotPerSec > 2*spec.BitrotPerSec || a.ReadCorrupt > 2*spec.ReadCorrupt || a.LostWrite > 2*spec.LostWrite || a.DiskBytes != spec.DiskBytes || a.DiskLatency != spec.DiskLatency {
			t.Fatalf("seed %d: swarm changed a disk setting it does not own or raised a disk rate beyond twice the configured one: %+v", seed, a)
		}
		mixes[fmt.Sprint(a)] = true
	}
	if len(mixes) < 20 {
		t.Fatalf("only %d distinct swarm mixes over 40 seeds", len(mixes))
	}
}

func TestRandomSideIsAProperNonEmptySubset(t *testing.T) {
	r := seeded(5)
	for _, names := range [][]string{{"a", "b"}, {"a", "b", "c", "d", "e"}} {
		for range 500 {
			side := randomSide(r, names)
			if len(side) == 0 || len(side) == len(names) {
				t.Fatalf("randomSide(%v) = %v, want both sides non-empty", names, side)
			}
			for _, n := range side {
				if !slices.Contains(names, n) {
					t.Fatalf("randomSide(%v) returned unknown node %q", names, n)
				}
			}
		}
	}
}

func TestStreamsAreIndependentAndReproducible(t *testing.T) {
	a, b, other := streams(42, "disk"), streams(42, "disk"), streams(42, "net")
	same := true
	for range 10 {
		x, y, z := a.Uint64(), b.Uint64(), other.Uint64()
		if x != y {
			t.Fatal("the same seed and stream name gave different values")
		}
		same = same && x == z
	}
	if same {
		t.Fatal("different stream names gave the same sequence")
	}
}

func TestCheckScheduleAcceptsGeneratedFaultsAndNamesEveryProblem(t *testing.T) {
	cfg := faultConfig()
	cfg.Faults.ClogPerSec, cfg.Faults.SlowPerSec, cfg.Faults.ResetPerSec = 0.3, 0.2, 0.2
	for seed := uint64(1); seed <= 20; seed++ {
		if err := cfg.CheckSchedule(Schedule{Faults: generateFaults(cfg, cfg.Faults, streams(seed, "faults"))}); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
	bad := []struct {
		f    Fault
		want string
	}{
		{Fault{At: Duration(time.Second), Kind: "meteor", Node: "n1"}, "unknown kind"},
		{Fault{At: Duration(time.Second), Kind: FaultCrash, Node: "n9", Until: Duration(2 * time.Second)}, `unknown node "n9"`},
		{Fault{At: Duration(2 * time.Second), Kind: FaultPause, Node: "n1", Until: Duration(time.Second)}, "before at"},
		{Fault{At: Duration(30 * time.Second), Kind: FaultCrash, Node: "n1", Until: Duration(31 * time.Second)}, "never fires"},
		{Fault{At: Duration(time.Second), Kind: FaultPartition, Until: Duration(2 * time.Second)}, "side or groups"},
		{Fault{At: Duration(time.Second), Kind: FaultClog, Mode: ClogStop, From: "n1", To: "n1", Until: Duration(2 * time.Second)}, "two different nodes"},
		{Fault{At: Duration(time.Second), Kind: FaultClog, Mode: "melt", Node: "n1", Dir: DirIn, Until: Duration(2 * time.Second)}, `mode "melt"`},
		{Fault{At: Duration(time.Second), Kind: FaultSlow, Node: "n1", Factor: 0.5, Until: Duration(2 * time.Second)}, "below 1"},
		{Fault{At: Duration(time.Second), Kind: FaultCrashIO, Node: "n1", Until: Duration(2 * time.Second)}, "op 0"},
		{Fault{At: Duration(time.Second), Kind: FaultBuggify, Node: "n1", Op: 1}, "needs site"},
		{Fault{At: Duration(time.Second), Kind: FaultClockJump}, "node is required"},
	}
	for _, c := range bad {
		err := cfg.CheckSchedule(Schedule{Faults: []Fault{c.f}})
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "fault 1 ") {
			t.Errorf("%+v: %v, want %q", c.f, err, c.want)
		}
	}
	late := Fault{At: Duration(5 * time.Second), Kind: FaultReset, Node: "n1", Until: Duration(5 * time.Second)}
	if err := cfg.CheckSchedule(Schedule{Faults: []Fault{late}, Duration: Duration(4 * time.Second)}); err == nil {
		t.Error("a fault after a shrunk schedule's duration was accepted")
	}
	edge := []Fault{
		{At: Duration(4 * time.Second), Kind: FaultReset, Node: "n1", Until: Duration(4 * time.Second)},
		{At: Duration(6 * time.Second), Kind: FaultCrashIO, Node: "n1", Op: 9, Until: Duration(7 * time.Second)},
	}
	if err := cfg.CheckSchedule(Schedule{Faults: edge, Duration: Duration(4 * time.Second)}); err != nil {
		t.Errorf("a fault at the end of the run or a disk-op fault recorded after it was rejected: %v", err)
	}
}

func TestSwarmScalesUDPRates(t *testing.T) {
	spec := FaultSpec{Swarm: true, UDPLoss: 0.2, UDPDup: 0.2}
	off, on := false, false
	for seed := range uint64(64) {
		got := spec.forSeed(seed)
		off = off || got.UDPLoss == 0
		on = on || (got.UDPLoss > 0 && got.UDPLoss != spec.UDPLoss)
		if got.UDPLoss > 2*spec.UDPLoss || got.UDPDup > 2*spec.UDPDup {
			t.Fatalf("seed %d rescaled past twice the rate: %+v", seed, got)
		}
	}
	if !off || !on {
		t.Fatalf("swarm off=%v rescaled=%v over 64 seeds, want both", off, on)
	}
}

func TestUDPFaultsPassScheduleChecksAndRatesAreBounded(t *testing.T) {
	cfg := defaultConfig()
	cfg.Nodes = []NodeSpec{{Name: "n1", Wasm: "x"}}
	cfg.Workload = NodeSpec{Name: "client", Wasm: "x"}
	good := Fault{At: Duration(time.Second), Kind: FaultUDPDrop, Node: "n1", To: "client", Op: 3}
	if err := cfg.CheckSchedule(Schedule{Faults: []Fault{good, {At: Duration(time.Second), Kind: FaultUDPDup, Node: "n1", To: "client", Op: 4}}}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.CheckSchedule(Schedule{Faults: []Fault{{Kind: FaultUDPDrop, Node: "n1"}}}); err == nil {
		t.Fatal("a udp_drop with op 0 passed the schedule check")
	}
	if !good.FiresAtOp() || !strings.Contains(good.Describe(), "drop n1's udp datagram #3 to client") {
		t.Fatalf("describe %q fires at op %v", good.Describe(), good.FiresAtOp())
	}
	cfg.Faults.UDPLoss = 1.5
	if err := cfg.validate(); err == nil {
		t.Fatal("udp_loss above 1 passed validation")
	}
}
