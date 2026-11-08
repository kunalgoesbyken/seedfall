package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func editFixture(t *testing.T) (config, schedule string) {
	t.Helper()
	dir := t.TempDir()
	config = filepath.Join(dir, "seedfall.json")
	if err := os.WriteFile(config, []byte(`{"duration": "10s", "nodes": [{"name": "server", "pkg": "./server"}], "workload": {"name": "client", "pkg": "./client"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	schedule = filepath.Join(dir, "seed-3.faults.json")
	faults := []engine.Fault{
		{At: engine.Duration(time.Second), Kind: engine.FaultCrashIO, Node: "server", Op: 40, Until: engine.Duration(1200 * time.Millisecond)},
		{At: engine.Duration(2 * time.Second), Kind: engine.FaultPartition, Side: []string{"client"}, Until: engine.Duration(3 * time.Second)},
		{At: engine.Duration(4 * time.Second), Kind: engine.FaultCrash, Node: "server", Until: engine.Duration(4500 * time.Millisecond)},
	}
	if err := engine.WriteScheduleFile(schedule, engine.ScheduleFile{Seed: 3, Schedule: engine.Schedule{Faults: faults}, ShrunkFrom: &engine.ScheduleSize{Faults: 9}, ShrinkRuns: 40}); err != nil {
		t.Fatal(err)
	}
	return config, schedule
}

func callEdit(t *testing.T, cs *mcp.ClientSession, args map[string]any) (faultsView, *mcp.CallToolResult) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_faults_edit", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var v faultsView
	if !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
	}
	return v, res
}

func TestMCPFaultsEditRemovesShiftsAndAdds(t *testing.T) {
	cs := connect(t)
	config, schedule := editFixture(t)
	out := filepath.Join(filepath.Dir(schedule), "hypothesis.json")
	v, res := callEdit(t, cs, map[string]any{
		"config": config, "seed": 3, "faults": schedule, "out": out,
		"remove": []int{2},
		"shift":  []map[string]any{{"fault": 1, "by": "+2"}, {"fault": 3, "by": "-1s"}},
		"add":    []map[string]any{{"at": "500ms", "kind": "pause", "node": "server", "until": "800ms"}},
	})
	if res.IsError {
		t.Fatalf("seedfall_faults_edit: %v", res.Content)
	}
	if !v.Wrote || v.File != filepath.ToSlash(out) || len(v.Faults) != 3 {
		t.Fatalf("edit view %+v", v)
	}
	got := []string{v.Faults[0].Fault.Kind, v.Faults[1].Fault.Kind, v.Faults[2].Fault.Kind}
	if strings.Join(got, ",") != "pause,crash_io,crash" || v.Faults[1].Fault.Op != 42 || time.Duration(v.Faults[2].Fault.At) != 3*time.Second || time.Duration(v.Faults[2].Fault.Until) != 3500*time.Millisecond {
		t.Fatalf("edited faults %+v", v.Faults)
	}
	written, err := engine.ReadScheduleFile(out)
	if err != nil || written.Seed != 3 || len(written.Faults) != 3 || written.ShrunkFrom != nil || written.ShrinkRuns != 0 {
		t.Fatalf("written schedule %+v %v", written, err)
	}
	if !strings.Contains(v.Replay, "-seed 3 -faults "+filepath.ToSlash(out)) {
		t.Fatalf("replay command %q", v.Replay)
	}
	listed, res := callEdit(t, cs, map[string]any{"config": config, "seed": 3, "faults": schedule})
	if res.IsError || listed.Wrote || len(listed.Faults) != 3 || listed.Faults[1].N != 2 {
		t.Fatalf("listing without edits %+v %v", listed, res.Content)
	}
}

func TestMCPFaultsEditRejectsBadEditsAndWritesNothing(t *testing.T) {
	cs := connect(t)
	config, schedule := editFixture(t)
	before, err := os.ReadFile(schedule)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		want string
		args map[string]any
	}{
		{`unknown node "nobody"`, map[string]any{"add": []map[string]any{{"at": "1s", "kind": "crash", "node": "nobody", "until": "2s"}}}},
		{"never fires", map[string]any{"shift": []map[string]any{{"fault": 3, "by": "+7s"}}}},
		{"by a count", map[string]any{"shift": []map[string]any{{"fault": 1, "by": "10ms"}}}},
		{"by a duration", map[string]any{"shift": []map[string]any{{"fault": 2, "by": "+3"}}}},
		{"no fault 4", map[string]any{"remove": []int{4}}},
		{"seed 3, not 5", map[string]any{"seed": 5}},
	} {
		args := map[string]any{"config": config, "seed": 3, "faults": schedule}
		for k, v := range c.args {
			args[k] = v
		}
		_, res := callEdit(t, cs, args)
		if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, c.want) {
			t.Errorf("%v: %v, want %q", c.args, res.Content, c.want)
		}
	}
	after, err := os.ReadFile(schedule)
	if err != nil || string(after) != string(before) {
		t.Fatalf("a rejected edit changed the schedule: %v", err)
	}
}
