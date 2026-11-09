package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLogFilterKeepsNodesWindowAndPattern(t *testing.T) {
	lines := []engine.TraceLine{
		{At: 0, Node: "server", Text: "    0.000s  start server#1"},
		{At: time.Second, Node: "client", Instance: "client#1", Output: `{"level":"INFO","msg":"put","key":"k1"}`, Text: `    1.000s  client#1| {"level":"INFO","msg":"put","key":"k1"}`},
		{At: 2 * time.Second, Text: "    2.000s  workload finished"},
		{At: 3 * time.Second, Node: "server", Text: "    3.000s  fault crash server#1, restart at 3.100s"},
	}
	f, err := newLogFilter("server", "crash", "1s", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.apply(lines); len(got) != 1 || got[0].At != 3*time.Second {
		t.Fatalf("server crash lines after 1s: %+v", got)
	}
	f, _ = newLogFilter("", "", "", "2s")
	if got := f.apply(lines); len(got) != 3 {
		t.Fatalf("lines up to 2s: %+v", got)
	}
	if f.unknownNodes([]string{"server"}) != "" {
		t.Fatal("no node filter named an unknown node")
	}
	f, _ = newLogFilter("server,nobody", "", "", "")
	if f.unknownNodes([]string{"server", "client"}) != "nobody" {
		t.Fatal("an unknown node was accepted")
	}
	f, err = newLogFilter(" server , client ", "", "0s", "0s")
	if err != nil || !slices.Equal(f.Nodes, []string{"server", "client"}) || f.To == nil {
		t.Fatalf("trimmed nodes and an explicit zero end: %+v, %v", f, err)
	}
	if got := f.apply(lines); len(got) != 1 || got[0].At != 0 {
		t.Fatalf("lines up to an explicit 0s: %+v", got)
	}
	for _, bad := range [][4]string{{"", "([", "", ""}, {"", "", "soon", ""}, {"", "", "", "later"}, {"", "", "-1s", ""}, {"", "", "", "-1s"}, {"", "", "2s", "1s"}} {
		if _, err := newLogFilter(bad[0], bad[1], bad[2], bad[3]); err == nil {
			t.Fatalf("filter %q was accepted", bad)
		}
	}
	v := viewLog(lines[1])
	if v.Fields["msg"] != "put" || v.Fields["key"] != "k1" || v.Node != "client" || v.AtNanos != int64(time.Second) {
		t.Fatalf("slog line view %+v", v)
	}
	if v := viewLog(engine.TraceLine{Output: "{not json"}); v.Fields != nil {
		t.Fatalf("a non-JSON line has fields %+v", v.Fields)
	}
}

func TestReadTraceTakesAFileOrAnArtifactsFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, engine.ArtifactTrace), []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("a\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := readTrace(dir)
	if err != nil || !slices.Equal(a, []string{"a", "b"}) {
		t.Fatalf("artifacts folder trace %q %v", a, err)
	}
	b, err := readTrace(other)
	if err != nil {
		t.Fatal(err)
	}
	if line, diverged := engine.FirstDivergence(a, b); !diverged || line != 1 {
		t.Fatalf("divergence at %d, %v", line, diverged)
	}
	if _, diverged := engine.FirstDivergence(a, a); diverged {
		t.Fatal("a trace diverges from itself")
	}
	if _, err := readTrace(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing trace has no error")
	}
}

func TestMCPListsLogsAndDebugAndRejectsBadFilters(t *testing.T) {
	cs := connect(t)
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"seedfall_logs", "seedfall_debug"} {
		if !slices.ContainsFunc(tools.Tools, func(tl *mcp.Tool) bool { return tl.Name == name }) {
			t.Errorf("%s is not listed", name)
		}
	}
	for _, c := range []struct {
		name, want string
		args       map[string]any
	}{
		{"seedfall_logs", "missing closing", map[string]any{"config": "seedfall.json", "seed": 1, "grep": "(["}},
		{"seedfall_logs", "negative", map[string]any{"config": "seedfall.json", "seed": 1, "limit": -1}},
		{"seedfall_debug", "invalid duration", map[string]any{"config": "seedfall.json", "seed": 1, "at": "soon"}},
	} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		if err != nil || !res.IsError {
			t.Fatalf("%s accepted %v: %v %v", c.name, c.args, err, res.Content)
		}
		if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, c.want) {
			t.Fatalf("%s %v: %s, want %q", c.name, c.args, text, c.want)
		}
	}
}
