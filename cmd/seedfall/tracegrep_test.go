package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSearchTraceMergesContextWindows(t *testing.T) {
	var lines []engine.TraceLine
	for i := range 12 {
		text := fmt.Sprintf("line %d", i+1)
		if i == 2 || i == 4 || i == 10 {
			text += " crash"
		}
		lines = append(lines, engine.TraceLine{At: time.Duration(i) * time.Millisecond, Node: "server", Text: text})
	}
	f, err := newLogFilter("", "crash", "", "")
	if err != nil {
		t.Fatal(err)
	}
	windows, matched := searchTrace(lines, f, 1, defaultLogLimit)
	if matched != 3 || len(windows) != 2 {
		t.Fatalf("%d matches in %d windows: %+v", matched, len(windows), windows)
	}
	var first []string
	for _, h := range windows[0] {
		first = append(first, fmt.Sprintf("%d:%t", h.N, h.Match))
	}
	if strings.Join(first, " ") != "2:false 3:true 4:false 5:true 6:false" || windows[1][0].N != 10 || len(windows[1]) != 3 || windows[1][1].At != "10ms" {
		t.Fatalf("windows %v then %+v", first, windows[1])
	}
	windows, matched = searchTrace(lines, f, 0, 1)
	if matched != 3 || len(windows) != 1 || len(windows[0]) != 1 || windows[0][0].N != 3 {
		t.Fatalf("limited to one match: %d %+v", matched, windows)
	}
}

func TestMCPTraceSearchRejectsBadArguments(t *testing.T) {
	cs := connect(t)
	for _, c := range []struct {
		want string
		args map[string]any
	}{
		{"pattern", map[string]any{"config": "seedfall.json", "seed": 1}},
		{"must not be negative", map[string]any{"config": "seedfall.json", "seed": 1, "pattern": "x", "context": -1}},
		{"missing closing", map[string]any{"config": "seedfall.json", "seed": 1, "pattern": "(["}},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "seedfall_trace_search", Arguments: c.args})
		if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, c.want) {
			t.Errorf("%v: %v %v, want %q", c.args, err, res.Content, c.want)
		}
	}
}
