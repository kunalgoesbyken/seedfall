package main

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	server, err := newMCPServer()
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestMCPListsInitPromptsAndResources(t *testing.T) {
	cs := connect(t)
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"seedfall_init", "seedfall_pin"} {
		if !slices.ContainsFunc(tools.Tools, func(tl *mcp.Tool) bool { return tl.Name == name }) {
			t.Errorf("%s is not listed", name)
		}
	}
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := readEmbedded("skills/*/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts.Prompts) != len(skills) || len(skills) == 0 {
		t.Errorf("%d prompts for %d skills", len(prompts.Prompts), len(skills))
	}
	for _, s := range skills {
		if want := path.Base(path.Dir(s.Path)); s.Meta["name"] != want {
			t.Errorf("%s: name %q must match its directory %q", s.Path, s.Meta["name"], want)
		}
	}
	got, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "seedfall-triage"})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("seedfall-triage prompt: %v", err)
	}
	read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: configSchemaURI})
	if err != nil {
		t.Fatal(err)
	}
	type property struct {
		Default    json.RawMessage `json:"default"`
		Properties map[string]struct {
			Default json.RawMessage `json:"default"`
			Enum    []string        `json:"enum"`
		} `json:"properties"`
	}
	var schema struct {
		Properties map[string]property `json:"properties"`
	}
	if err := json.Unmarshal([]byte(read.Contents[0].Text), &schema); err != nil {
		t.Fatal(err)
	}
	if string(schema.Properties["duration"].Default) != `"30s"` {
		t.Errorf("duration default = %s", schema.Properties["duration"].Default)
	}
	var modes []string
	if err := json.Unmarshal(schema.Properties["faults"].Properties["clog_modes"].Default, &modes); err != nil || !slices.Equal(modes, []string{"stop", "slow", "jam"}) {
		t.Errorf("faults.clog_modes default = %v (%v)", modes, err)
	}
	if _, ok := schema.Properties["links"]; !ok {
		t.Error("schema has no links field")
	}
	if _, ok := schema.Properties["faults"].Properties["read_corrupt"]; !ok {
		t.Error("schema has no faults.read_corrupt field")
	}
	for _, field := range []string{"workloads", "finally", "liveness"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("schema has no %s field", field)
		}
	}
	if _, ok := schema.Properties["liveness"].Properties["within"]; !ok {
		t.Error("schema has no liveness.within field")
	}
	if _, ok := schema.Properties["faults"].Properties["include_workload"]; !ok {
		t.Error("schema has no faults.include_workload field")
	}
	if preset := schema.Properties["faults"].Properties["preset"]; !slices.Equal(preset.Enum, engine.PresetNames()) {
		t.Errorf("faults.preset enum %v, want %v", preset.Enum, engine.PresetNames())
	}
	for _, field := range []string{"extends", "profiles"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("schema has no %s field", field)
		}
	}
	if model := schema.Properties["checker"].Properties["model"]; string(model.Default) != `"kv"` || !slices.Equal(model.Enum, engine.Models()) {
		t.Errorf("checker.model default %s enum %v", model.Default, model.Enum)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "seedfall://docs/properties"}); err != nil {
		t.Error(err)
	}
}

func callInit(t *testing.T, cs *mcp.ClientSession, dir string) initView {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_init", Arguments: map[string]any{"dir": dir}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("seedfall_init: %v", res.Content)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var v initView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestInitScaffoldsANetworkedMain(t *testing.T) {
	cs := connect(t)
	dir := t.TempDir()
	write := func(rel, text string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/svc\n\ngo 1.25\n")
	write("cmd/svc/main.go", "package main\n\nimport \"net\"\n\nfunc main() { net.Listen(\"tcp\", \":9000\") }\n")
	write("tool/main.go", "package main\n\nfunc main() {}\n")
	v := callInit(t, cs, dir)
	if len(v.Config.Nodes) != 1 || v.Config.Nodes[0].Name != "svc" || v.Config.Nodes[0].Pkg != "./cmd/svc" {
		t.Errorf("nodes = %+v", v.Config.Nodes)
	}
	if !v.Config.NetOverlay || v.RequiresSeedfall || len(v.Blockers) != 0 {
		t.Errorf("overlay %t, requires seedfall %t, blockers %v", v.Config.NetOverlay, v.RequiresSeedfall, v.Blockers)
	}
	if !slices.Equal(v.Wrote, []string{"seedfall.json", "seedfall/workload/main.go"}) {
		t.Errorf("wrote %v", v.Wrote)
	}
	cfg, err := engine.LoadConfig(filepath.Join(dir, "seedfall.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workload.Pkg != "./seedfall/workload" {
		t.Errorf("workload pkg = %q", cfg.Workload.Pkg)
	}
	for name := range v.Profiles {
		p, err := engine.Load(engine.ConfigRef{Path: filepath.Join(dir, "seedfall.json"), Profile: name})
		if err != nil {
			t.Errorf("profile %s: %v", name, err)
			continue
		}
		if reflect.DeepEqual(p.Faults, cfg.Faults) {
			t.Errorf("profile %s does not change the faults", name)
		}
	}
	again := callInit(t, cs, dir)
	if len(again.Wrote) != 0 || len(again.Kept) != 2 {
		t.Errorf("second init wrote %v, kept %v", again.Wrote, again.Kept)
	}
}

func TestMCPChecksAStoredHistory(t *testing.T) {
	cs := connect(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "seed-1.history.jsonl")
	lines := `{"type":"invoke","process":1,"f":"put","key":"x","value":"1","time":0,"id":0}
{"type":"ok","process":1,"f":"put","key":"x","value":"","time":10,"id":0}
{"type":"invoke","process":2,"f":"get","key":"x","time":20,"id":1}
{"type":"ok","process":2,"f":"get","key":"x","value":"","time":30,"id":1}
`
	if err := os.WriteFile(file, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_check", Arguments: map[string]any{"history": file}})
	if err != nil || res.IsError {
		t.Fatalf("seedfall_check: %v %v", err, res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var v checkView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.Ok || v.Model != "kv" || v.Operations != 2 || v.Timeline != filepath.Join(dir, "seed-1.history.html") {
		t.Fatalf("check view %+v", v)
	}
	if _, err := os.Stat(v.Timeline); err != nil {
		t.Fatal(err)
	}
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_check", Arguments: map[string]any{"history": file, "model": "queue"}})
	if err != nil || !res.IsError {
		t.Fatalf("a kv history checked as a queue: %v %v", err, res.Content)
	}
}

func TestMCPReadsFindingsAndRunHistory(t *testing.T) {
	cs := connect(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "seedfall.json")
	rep := map[string]any{
		"config": config, "seeds": "1-50", "seeds_run": 50, "seeds_failed": 2,
		"findings":   []map[string]any{{"key": "assert · client", "status": "new", "failing_seeds": 2, "rate": 0.04, "example_seeds": []uint64{3, 9}}},
		"properties": []map[string]any{{"kind": "sometimes", "message": "torn tail", "problem": "never true"}, {"kind": "always", "message": "fine"}},
	}
	b, _ := json.Marshal(rep)
	if err := os.WriteFile(filepath.Join(dir, "report.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	var history []byte
	for _, e := range []report.Entry{{Config: config, Seeds: 50, Findings: map[string]int{"assert · client": 2}}, {Config: "other.json", Seeds: 10}} {
		line, _ := json.Marshal(e)
		history = append(append(history, line...), '\n')
	}
	if err := os.MkdirAll(filepath.Join(dir, ".seedfall"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".seedfall", "history.jsonl"), history, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_findings", Arguments: map[string]any{"report": filepath.Join(dir, "report.html")}})
	if err != nil || res.IsError {
		t.Fatalf("seedfall_findings: %v %v", err, res.Content)
	}
	b, _ = json.Marshal(res.StructuredContent)
	var v findingsView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Findings) != 1 || v.Findings[0].Count != 2 || len(v.Properties) != 1 || v.Properties[0].Message != "torn tail" || len(v.Runs) != 1 || v.Runs[0].Seeds != 50 {
		t.Fatalf("findings view %+v", v)
	}
}

func TestMCPMutateRejectsAnUnknownMutant(t *testing.T) {
	cs := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_mutate", Arguments: map[string]any{"config": "seedfall.json", "only": "fsync_noop,typo"}})
	if err != nil || !res.IsError {
		t.Fatalf("an unknown mutant was accepted: %v %v", err, res.Content)
	}
}

func TestMCPMutateRejectsOnlyWithApp(t *testing.T) {
	cs := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_mutate", Arguments: map[string]any{"config": "seedfall.json", "only": "fsync_noop", "app": true}})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "-app") {
		t.Fatalf("only was accepted with app: %v %v", err, res.Content)
	}
}
