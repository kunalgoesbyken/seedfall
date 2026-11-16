package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func envGuestConfig(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a wasip1 guest")
	}
	dir := t.TempDir()
	wasm := filepath.Join(dir, "guest.wasm")
	build := exec.Command("go", "build", "-o", wasm, "./testdata/guest")
	build.Dir = filepath.Join("..", "..", "engine")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, out)
	}
	cfg := map[string]any{
		"nodes":    []map[string]any{{"name": "store", "wasm": wasm, "args": []string{"-role", "store"}}},
		"workload": map[string]any{"name": "client", "wasm": wasm, "args": []string{"-role", "client", "-history"}},
		"duration": "10s",
		"faults":   map[string]any{"crash_per_sec": 1, "restart_after": []string{"10ms", "100ms"}, "torn_writes": true},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "seedfall.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func envExchange(t *testing.T, h *envHost, requests ...envRequest) []envResponse {
	t.Helper()
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for _, r := range requests {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := h.serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	var responses []envResponse
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var res envResponse
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			t.Fatalf("response %q: %v", line, err)
		}
		responses = append(responses, res)
	}
	if len(responses) != len(requests) {
		t.Fatalf("%d requests got %d responses", len(requests), len(responses))
	}
	return responses
}

func TestEnvProtocolRejectsWhatItCannotDo(t *testing.T) {
	h := newEnvHost(1)
	defer h.close()
	got := envExchange(t, h,
		envRequest{Cmd: "step", Advance: "1s"},
		envRequest{Cmd: "observe"},
		envRequest{Cmd: "verify", Seeds: []uint64{1}},
		envRequest{Cmd: "nonsense"},
	)
	for i, want := range []string{"no episode", "no episode", "no episode", `unknown cmd "nonsense"`} {
		if got[i].OK || !strings.Contains(got[i].Error, want) {
			t.Fatalf("response %d is %+v, want an error with %q", i, got[i], want)
		}
	}
}

func TestEnvEpisodeVerifiesToTheSameHash(t *testing.T) {
	config := envGuestConfig(t)
	h := newEnvHost(2)
	defer h.close()
	got := envExchange(t, h,
		envRequest{Cmd: "reset", Config: config, Seed: 3},
		envRequest{Cmd: "step", Advance: "300ms"},
		envRequest{Cmd: "step", Op: &engine.Op{Kind: "crash", Node: "store"}},
		envRequest{Cmd: "step", Advance: "500ms"},
		envRequest{Cmd: "step", Op: &engine.Op{Kind: "restart", Node: "store"}},
		envRequest{Cmd: "step", Op: &engine.Op{Kind: "crash", Node: "client"}},
		envRequest{Cmd: "step", Op: &engine.Op{Kind: "crash", Node: "store"}, Advance: "1s"},
		envRequest{Cmd: "observe"},
		envRequest{Cmd: "state"},
		envRequest{Cmd: "finish"},
		envRequest{Cmd: "step", Advance: "1s"},
	)
	for i, res := range got[:5] {
		if !res.OK || res.Observation == nil {
			t.Fatalf("response %d: %+v", i, res)
		}
	}
	for i, want := range []string{"takes no faults", "either op or advance"} {
		if got[5+i].OK || !strings.Contains(got[5+i].Error, want) {
			t.Fatalf("response %d is %+v, want an error with %q", 5+i, got[5+i], want)
		}
	}
	if len(got[8].Observation.Log) != 0 {
		t.Fatal("state returned log lines")
	}
	verdict := got[9].Verdict
	if !got[9].OK || verdict == nil || len(verdict.Ops) == 0 {
		t.Fatalf("finish: %+v", got[9])
	}
	if got[10].OK {
		t.Fatal("a step after finish worked")
	}
	again := envExchange(t, h, envRequest{Cmd: "verify", Seeds: []uint64{3, 4, 5}})[0]
	if !again.OK || len(again.Verify.Seeds) != 3 {
		t.Fatalf("verify: %+v", again)
	}
	first := again.Verify.Seeds[0]
	if first.Seed != 3 || first.Hash != verdict.Hash || first.Passed != verdict.Passed {
		t.Fatalf("the episode's seed gave hash %s and pass %v, finish gave %s and %v", first.Hash, first.Passed, verdict.Hash, verdict.Passed)
	}
	passed := 0
	for _, s := range again.Verify.Seeds {
		if s.Passed {
			passed++
		}
	}
	if again.Verify.Passed != passed || again.Verify.Reward != float64(passed)/3 {
		t.Fatalf("reward %v with %d passed, counted %d of 3", again.Verify.Reward, again.Verify.Passed, passed)
	}
}

func TestMCPEnvToolsRunAnEpisode(t *testing.T) {
	config := envGuestConfig(t)
	cs := connect(t)
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		out, ok := res.StructuredContent.(map[string]any)
		if !ok || out["ok"] != true {
			t.Fatalf("%s returned %+v", name, res.StructuredContent)
		}
		return out
	}
	call("seedfall_env_reset", map[string]any{"config": config, "seed": 3})
	call("seedfall_env_step", map[string]any{"kind": "crash", "node": "store"})
	call("seedfall_env_step", map[string]any{"advance": "400ms"})
	call("seedfall_env_step", map[string]any{"kind": "restart", "node": "store"})
	call("seedfall_env_observe", map[string]any{})
	verdict := call("seedfall_env_finish", map[string]any{})["verdict"].(map[string]any)
	verify := call("seedfall_env_verify", map[string]any{"seeds": []uint64{4}})["verify"].(map[string]any)
	first := verify["seeds"].([]any)[0].(map[string]any)
	if first["determinism_hash"] != verdict["determinism_hash"] {
		t.Fatalf("verify gave hash %v, finish gave %v", first["determinism_hash"], verdict["determinism_hash"])
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "seedfall_env_step", Arguments: map[string]any{"advance": "1s"}})
	if err != nil || !res.IsError {
		t.Fatalf("a step after finish gave %+v, %v", res, err)
	}
}
