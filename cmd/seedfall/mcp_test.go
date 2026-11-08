package main

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
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
	if !slices.ContainsFunc(tools.Tools, func(tl *mcp.Tool) bool { return tl.Name == "seedfall_init" }) {
		t.Error("seedfall_init is not listed")
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
	again := callInit(t, cs, dir)
	if len(again.Wrote) != 0 || len(again.Kept) != 2 {
		t.Errorf("second init wrote %v, kept %v", again.Wrote, again.Kept)
	}
}
