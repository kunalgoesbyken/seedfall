package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultContext = 3

type traceSearchArgs struct {
	seedArgs
	Pattern  string `json:"pattern" jsonschema:"regular expression to find in the trace lines"`
	Context  *int   `json:"context,omitempty" jsonschema:"trace lines of any node to return before and after each match, default 3"`
	Node     string `json:"node,omitempty" jsonschema:"only match lines of these nodes, comma separated"`
	From     string `json:"from,omitempty" jsonschema:"only match lines at or after this virtual time, for example 1.5s"`
	To       string `json:"to,omitempty" jsonschema:"only match lines at or before this virtual time"`
	NetTrace bool   `json:"net_trace,omitempty" jsonschema:"also trace every connection, message sent and message read"`
	Limit    int    `json:"limit,omitempty" jsonschema:"most matches to return, the first ones, default 200"`
}

type traceHit struct {
	N     int  `json:"n"`
	Match bool `json:"match"`
	logView
}

type traceSearchView struct {
	Seed    uint64       `json:"seed"`
	Passed  bool         `json:"passed"`
	Reason  string       `json:"reason,omitempty"`
	Total   int          `json:"trace_lines"`
	Matched int          `json:"matched"`
	Windows [][]traceHit `json:"windows"`
}

func searchTrace(lines []engine.TraceLine, f logFilter, around, limit int) ([][]traceHit, int) {
	windows := [][]traceHit{}
	matched, last := 0, -1
	for i, l := range lines {
		if !f.keep(l) {
			continue
		}
		matched++
		if matched > limit {
			continue
		}
		if last < i-around-1 || len(windows) == 0 {
			windows = append(windows, nil)
		}
		w := &windows[len(windows)-1]
		for j := max(last+1, i-around); j <= min(len(lines)-1, i+around); j++ {
			*w = append(*w, traceHit{N: j + 1, Match: f.keep(lines[j]), logView: viewLog(lines[j])})
		}
		last = max(last, min(len(lines)-1, i+around))
	}
	return windows, matched
}

func traceSearch(a traceSearchArgs) (traceSearchView, error) {
	around := defaultContext
	if a.Context != nil {
		around = *a.Context
	}
	if a.Pattern == "" {
		return traceSearchView{}, errors.New("pattern is required")
	}
	if around < 0 || a.Limit < 0 {
		return traceSearchView{}, fmt.Errorf("context %d and limit %d must not be negative", around, a.Limit)
	}
	filter, err := newLogFilter(a.Node, a.Pattern, a.From, a.To)
	if err != nil {
		return traceSearchView{}, err
	}
	res, err := recordedRun(&a.configArgs, filter, a.Seed, a.Faults, a.NetTrace)
	if err != nil {
		return traceSearchView{}, err
	}
	out := traceSearchView{Seed: a.Seed, Passed: !res.Failed, Reason: res.Reason, Total: len(res.Timeline.Logs)}
	out.Windows, out.Matched = searchTrace(res.Timeline.Logs, filter, around, cmp.Or(a.Limit, defaultLogLimit))
	return out, nil
}

func toolTraceSearch(_ context.Context, _ *mcp.CallToolRequest, a traceSearchArgs) (*mcp.CallToolResult, traceSearchView, error) {
	out, err := traceSearch(a)
	return nil, out, err
}

func cmdTraceGrep(args []string) error {
	fs := newFlags("trace grep")
	var a traceSearchArgs
	a.register(fs)
	seed := seedFlag(fs)
	fs.StringVar(&a.Faults, "faults", "", "fault schedule from seedfall shrink")
	fs.StringVar(&a.Pattern, "e", "", "regular expression to find in the trace lines")
	around := fs.Int("context", defaultContext, "trace lines of any node to show before and after each match")
	fs.StringVar(&a.Node, "node", "", "only match lines of these nodes, comma separated")
	fs.StringVar(&a.From, "from", "", "only match lines at or after this virtual time, for example 1.5s")
	fs.StringVar(&a.To, "to", "", "only match lines at or before this virtual time")
	fs.IntVar(&a.Limit, "limit", defaultLogLimit, "most matches to show, the first ones")
	fs.BoolVar(&a.NetTrace, "net-trace", false, "also trace every connection, message sent and message read")
	jsonOut := fs.Bool("json", false, "print the result as one JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a.Seed, a.Context = *seed, around
	out, err := traceSearch(a)
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	for i, w := range out.Windows {
		if i > 0 {
			fmt.Println("--")
		}
		for _, h := range w {
			mark := " "
			if h.Match {
				mark = ">"
			}
			fmt.Printf("%s%5d %s\n", mark, h.N, h.Line)
		}
	}
	verdict := "ok"
	if !out.Passed {
		verdict = "FAIL " + out.Reason
	}
	fmt.Fprintf(os.Stderr, "%d matches of %d trace lines, %d shown; seed %d: %s\n", out.Matched, out.Total, min(out.Matched, cmp.Or(a.Limit, defaultLogLimit)), a.Seed, verdict)
	return nil
}
