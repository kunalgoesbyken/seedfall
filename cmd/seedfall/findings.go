package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/kunalgoesbyken/seedfall/report"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var findingStatuses = []report.Status{report.StatusNew, report.StatusOngoing, report.StatusRegressed, report.StatusResolved, report.StatusUnconfirmed}

type findingsArgs struct {
	Report  string `json:"report,omitempty" jsonschema:"triage report written by seedfall run -report, as report.html or report.json; default .seedfall/report.html"`
	History string `json:"history,omitempty" jsonschema:"run history file, default .seedfall/history.jsonl next to the report's config"`
	Status  string `json:"status,omitempty" jsonschema:"only findings with these statuses, comma separated: new, ongoing, regressed, resolved, not seen"`
}

type findingsView struct {
	Report     string            `json:"report"`
	Config     string            `json:"config"`
	Seeds      string            `json:"seeds"`
	SeedsRun   int               `json:"seeds_run"`
	Failed     int               `json:"seeds_failed"`
	Findings   []report.Finding  `json:"findings"`
	Cleared    []report.Finding  `json:"previous_findings_not_seen"`
	Properties []report.Property `json:"unmet_properties"`
	Runs       []report.Entry    `json:"runs"`
}

func toolFindings(_ context.Context, _ *mcp.CallToolRequest, a findingsArgs) (*mcp.CallToolResult, findingsView, error) {
	out, err := findings(a)
	return nil, out, err
}

func findings(a findingsArgs) (findingsView, error) {
	keep, err := statusFilter(a.Status)
	if err != nil {
		return findingsView{}, err
	}
	path := cmp.Or(a.Report, defaultReport)
	rep, err := report.Load(path)
	if err != nil {
		return findingsView{}, err
	}
	runs, err := report.ReadHistory(reportHistory(rep, a.History), rep.Config)
	if err != nil {
		return findingsView{}, err
	}
	out := findingsView{Report: path, Config: rep.Config, Seeds: rep.Seeds, SeedsRun: rep.Total, Failed: rep.Failed, Findings: filterFindings(rep.Findings, keep), Cleared: filterFindings(rep.Cleared, keep), Runs: runs}
	for _, p := range rep.Properties {
		if p.Problem != "" {
			out.Properties = append(out.Properties, p)
		}
	}
	return out, nil
}

func statusFilter(s string) ([]report.Status, error) {
	if s == "" {
		return findingStatuses, nil
	}
	var keep []report.Status
	for _, name := range strings.Split(s, ",") {
		st := report.Status(strings.TrimSpace(name))
		if !slices.Contains(findingStatuses, st) {
			return nil, fmt.Errorf("unknown finding status %q, want one of %q", st, findingStatuses)
		}
		keep = append(keep, st)
	}
	return keep, nil
}

func filterFindings(fs []report.Finding, keep []report.Status) []report.Finding {
	out := []report.Finding{}
	for _, f := range fs {
		if slices.Contains(keep, f.Status) {
			out = append(out, f)
		}
	}
	return out
}

func cmdFindings(args []string) error {
	fs := newFlags("findings")
	var a findingsArgs
	fs.StringVar(&a.Report, "report", defaultReport, "triage report written by seedfall run -report")
	fs.StringVar(&a.History, "history", "", "run history (default: .seedfall/history.jsonl next to the report's config)")
	fs.StringVar(&a.Status, "status", "", "only findings with these statuses, comma separated: new, ongoing, regressed, resolved, not seen")
	jsonOut := fs.Bool("json", false, "print the findings as one JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := findings(a)
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	fmt.Printf("%s: %d of %d seeds failed (seeds %s of %s), %d runs in history\n", out.Report, out.Failed, out.SeedsRun, out.Seeds, out.Config, len(out.Runs))
	for _, f := range out.Findings {
		fmt.Printf("  %-9s %-40s %5d seeds %6.2f%%  e.g. seed %s\n", f.Status, f.Key, f.Count, 100*f.Rate, exampleSeed(f))
		if f.Example != nil {
			fmt.Printf("            %s\n            replay: %s\n", f.Example.Reason, f.Example.Replay)
		}
	}
	for _, f := range out.Cleared {
		fmt.Printf("  %-9s %-40s %s\n", f.Status, f.Key, f.Note)
	}
	for _, p := range out.Properties {
		fmt.Printf("  property  %-9s %s <- %s\n", p.Kind, p.Message, p.Problem)
	}
	return nil
}

func exampleSeed(f report.Finding) string {
	if len(f.Seeds) == 0 {
		return "-"
	}
	return fmt.Sprint(f.Seeds[0])
}
