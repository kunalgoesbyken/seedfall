package main

import (
	"context"

	"github.com/kunalgoesbyken/seedfall/report"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type findingsArgs struct {
	Report  string `json:"report" jsonschema:"triage report written by seedfall run -report, as report.html or report.json"`
	History string `json:"history,omitempty" jsonschema:"run history file, default .seedfall/history.jsonl next to the report's config"`
}

type findingsView struct {
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
	rep, err := report.Load(a.Report)
	if err != nil {
		return nil, findingsView{}, err
	}
	runs, err := report.ReadHistory(reportHistory(rep, a.History), rep.Config)
	if err != nil {
		return nil, findingsView{}, err
	}
	out := findingsView{Config: rep.Config, Seeds: rep.Seeds, SeedsRun: rep.Total, Failed: rep.Failed, Findings: rep.Findings, Cleared: rep.Cleared, Runs: runs}
	for _, p := range rep.Properties {
		if p.Problem != "" {
			out.Properties = append(out.Properties, p)
		}
	}
	return nil, out, nil
}
