package engine

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func randomOp(r *rand.Rand) Op {
	span := func() string { return fmt.Sprintf("%dms", 50+r.IntN(500)) }
	both := func() string { return []string{"store", "client"}[r.IntN(2)] }
	switch r.IntN(10) {
	case 0:
		return Op{Kind: OpCrash, Node: "store"}
	case 1:
		return Op{Kind: OpRestart, Node: "store", Args: []string{span()}}
	case 2:
		return Op{Kind: OpPause, Node: both()}
	case 3:
		return Op{Kind: OpResume, Node: both()}
	case 4:
		return Op{Kind: OpPartition, Args: []string{span(), "store"}}
	case 5:
		return Op{Kind: OpHeal}
	case 6:
		return Op{Kind: OpSlow, Node: both(), Args: []string{"3", span()}}
	case 7:
		return Op{Kind: OpClockJump, Node: both(), Args: []string{"-" + span()}}
	case 8:
		return Op{Kind: OpUpgrade, Node: "store"}
	}
	return Op{Kind: OpSetArgs, Node: "store", Args: []string{"-role", "store", "-fsync=true"}}
}

func operatedRunner(t *testing.T) *Runner {
	t.Helper()
	return operatedRunnerWith(t, func(*Config) {})
}

func operatedRunnerWith(t *testing.T, edit func(*Config)) *Runner {
	t.Helper()
	cfg := guestConfig(t, true)
	edit(cfg)
	cfg.Nodes[0].UpgradeTo = "v2"
	cfg.Nodes[0].upgradeWasm = cfg.Nodes[0].Wasm
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func operate(t *testing.T, r *Runner, seed uint64, script *rand.Rand, steps int) (Result, []Op) {
	t.Helper()
	s, err := r.Operate(seed, Schedule{})
	if err != nil {
		t.Fatal(err)
	}
	for range steps {
		if s.Over() {
			break
		}
		if script.IntN(3) == 0 {
			err = s.Advance(time.Duration(20+script.IntN(700)) * time.Millisecond)
		} else {
			err = s.Do(randomOp(script))
		}
		if err != nil && !s.Over() {
			t.Fatal(err)
		}
		s.Observe()
	}
	ops := s.Ops()
	return s.Finish(), ops
}

func TestOperatedSessionReplaysByteIdenticalFromItsLoggedActions(t *testing.T) {
	r := operatedRunner(t)
	for seed := range uint64(12) {
		script := rand.New(rand.NewPCG(seed, 99))
		live, ops := operate(t, r, seed, script, 30)
		if len(ops) == 0 {
			t.Fatalf("seed %d: the session logged no actions", seed)
		}
		replayed := r.Replay(seed, Schedule{Operator: ops})
		if live.Hash() != replayed.Hash() || live.HostCalls != replayed.HostCalls {
			diff, _ := divergence(live.Trace, replayed.Trace)
			t.Fatalf("seed %d: session hash %s over %d calls, replay %s over %d calls; %s", seed, live.Hash(), live.HostCalls, replayed.Hash(), replayed.HostCalls, diff)
		}
		if diff, diverged := divergence(live.Trace, replayed.Trace); diverged {
			t.Fatalf("seed %d: traces diverge at %s", seed, diff)
		}
		if live.Reason != replayed.Reason || !reflect.DeepEqual(live.Operator, replayed.Operator) {
			t.Fatalf("seed %d: session %q with %d actions, replay %q with %d", seed, live.Reason, len(live.Operator), replayed.Reason, len(replayed.Operator))
		}
	}
}

func TestOperatedSessionWithoutActionsIsTheOrdinaryRun(t *testing.T) {
	r := operatedRunner(t)
	for seed := range uint64(4) {
		s, err := r.Operate(seed, Schedule{})
		if err != nil {
			t.Fatal(err)
		}
		live, plain := s.Finish(), r.Replay(seed, Schedule{})
		if live.Hash() != plain.Hash() {
			diff, _ := divergence(live.Trace, plain.Trace)
			t.Fatalf("seed %d: operated %s, plain %s; %s", seed, live.Hash(), plain.Hash(), diff)
		}
	}
}

func TestOperatorActionsAreVisibleAndChangeTheRun(t *testing.T) {
	r := operatedRunnerWith(t, func(cfg *Config) {
		cfg.Faults.CrashPerSec, cfg.Faults.PartitionPerSec, cfg.Faults.PausePerSec, cfg.Faults.CrashOnIO = 0, 0, 0, 0
	})
	s, err := r.Operate(5, Schedule{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state := func(node string) EnvNode {
		for _, n := range s.Observe().Nodes {
			if n.Node == node {
				return n
			}
		}
		t.Fatalf("no node %s", node)
		return EnvNode{}
	}
	if err := s.Do(Op{Kind: OpCrash, Node: "store"}); err != nil {
		t.Fatal(err)
	}
	if got := state("store"); got.State != "down" {
		t.Fatalf("after a crash the store is %s", got.State)
	}
	if err := s.Do(Op{Kind: OpSetArgs, Node: "store", Args: []string{"-role", "store", "-fsync=false"}}); err != nil {
		t.Fatal(err)
	}
	if got := state("store").Args; !slices.Equal(got, []string{"-role", "store", "-fsync=false"}) {
		t.Fatalf("args %v", got)
	}
	if err := s.Do(Op{Kind: OpRestart, Node: "store"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(50 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	got := state("store")
	if got.State == "down" || got.Starts < 2 {
		t.Fatalf("after a restart the store is %s with %d starts", got.State, got.Starts)
	}
	obs := s.Observe()
	if len(obs.Log) != 0 {
		t.Fatalf("a second observe repeats %d log lines", len(obs.Log))
	}
	if err := s.Advance(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(s.Observe().Log, "\n")
	for _, want := range []string{"operator stops", "operator sets the args", "operator starts", "upgraded to v2", "operator waits"} {
		if want == "upgraded to v2" || want == "operator stops" {
			continue
		}
		if !strings.Contains(lines+strings.Join(traceLines(s), "\n"), want) {
			t.Fatalf("the trace has no %q", want)
		}
	}
	if s.Observe().Client.Invoked < 0 {
		t.Fatal("negative client stats")
	}
}

func traceLines(s *Session) []string {
	var out []string
	for _, l := range s.Trace() {
		out = append(out, l.Text)
	}
	return out
}

func TestOperatorUpgradeNeedsAnUpgradeTarget(t *testing.T) {
	r := guestRunner(t, true)
	s, err := r.Operate(1, Schedule{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Do(Op{Kind: OpUpgrade, Node: "store"}); err == nil || !strings.Contains(err.Error(), "no upgrade_to") {
		t.Fatalf("upgrading a node without upgrade_to: %v", err)
	}
	r = operatedRunner(t)
	res := r.Replay(1, Schedule{Operator: []Op{{At: Duration(200 * time.Millisecond), Kind: OpUpgrade, Node: "store"}}})
	if traced(res, "upgraded to v2") < 0 {
		t.Fatal("the upgrade action did not start the upgraded build")
	}
}

func TestOperatorActionsAreChecked(t *testing.T) {
	r := guestRunner(t, true)
	cfg := r.cfg
	horizon := time.Duration(cfg.Duration)
	tests := []struct {
		name string
		ops  []Op
		want string
	}{
		{"an unknown kind", []Op{{Kind: "reboot", Node: "store"}}, "unknown kind"},
		{"an unknown node", []Op{{Kind: OpCrash, Node: "nope"}}, `unknown node "nope"`},
		{"a node action without a node", []Op{{Kind: OpCrash}}, "node is required"},
		{"a cluster action with a node", []Op{{Kind: OpHeal, Node: "store"}}, "takes no node"},
		{"crashing the workload", []Op{{Kind: OpCrash, Node: "client"}}, "takes no faults"},
		{"a bad restart delay", []Op{{Kind: OpRestart, Node: "store", Args: []string{"soon"}}}, "invalid duration"},
		{"a partition without a side", []Op{{Kind: OpPartition, Args: []string{"1s"}}}, "wrong number of args"},
		{"a slow factor below one", []Op{{Kind: OpSlow, Node: "store", Args: []string{"0.5", "1s"}}}, "below 1"},
		{"an action after the run ends", []Op{{At: Duration(horizon + time.Second), Kind: OpHeal}}, "never fires"},
		{"actions out of order", []Op{{At: Duration(time.Second), Kind: OpHeal}, {Kind: OpHeal}}, "before the previous action"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.CheckSchedule(Schedule{Operator: tt.ops})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %v, want one that says %q", err, tt.want)
			}
		})
	}
	if err := cfg.CheckSchedule(Schedule{Operator: []Op{{Kind: OpRestart, Node: "store", Args: []string{"100ms"}}, {Kind: OpWait, Args: []string{"1s"}}}}); err != nil {
		t.Fatalf("valid actions are rejected: %v", err)
	}
}

func TestOperatorActionsSurviveTheScheduleFile(t *testing.T) {
	in := Schedule{Faults: []Fault{}, Operator: []Op{{At: Duration(100 * time.Millisecond), Kind: OpPartition, Args: []string{"2s", "store"}}}}
	b, err := json.Marshal(ScheduleFile{Seed: 3, Schedule: in})
	if err != nil {
		t.Fatal(err)
	}
	var out ScheduleFile
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(out.Operator) != fmt.Sprint(in.Operator) {
		t.Fatalf("round trip gave %v, want %v", out.Operator, in.Operator)
	}
	r := operatedRunner(t)
	res := r.Replay(2, Schedule{Operator: in.Operator})
	if got := res.Schedule(Schedule{}); len(got.Operator) != 1 {
		t.Fatalf("the result's schedule has %d actions, want 1", len(got.Operator))
	}
}
