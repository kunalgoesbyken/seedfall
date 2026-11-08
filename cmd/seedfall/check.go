package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kunalgoesbyken/seedfall/engine"
)

type historyFiles struct{ History, Timeline string }

func saveHistory(res engine.Result) historyFiles {
	var out historyFiles
	if len(res.History.Ops) == 0 {
		return out
	}
	base := filepath.Join(".seedfall", fmt.Sprintf("seed-%d.history", res.Seed))
	if os.MkdirAll(filepath.Dir(base), 0o755) != nil {
		return out
	}
	if writeFile(base+".jsonl", func(w io.Writer) error { return engine.WriteHistory(w, res.History.Ops) }) == nil {
		out.History = base + ".jsonl"
	}
	if res.History.Checked && !res.History.Ok && res.History.HasTimeline() && writeFile(base+".html", res.History.Visualize) == nil {
		out.Timeline = base + ".html"
	} else {
		os.Remove(base + ".html")
	}
	return out
}

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

func (h historyFiles) print() {
	if h.History != "" {
		fmt.Printf("\nhistory: %s\n", h.History)
	}
	if h.Timeline != "" {
		fmt.Printf("linearizability timeline: %s\n", h.Timeline)
	}
}

type checkView struct {
	Model      string           `json:"model"`
	Checked    bool             `json:"checked"`
	Ok         bool             `json:"ok"`
	Operations int              `json:"operations_checked"`
	Summary    string           `json:"summary"`
	Anomalies  []engine.Anomaly `json:"anomalies,omitempty"`
	Timeline   string           `json:"timeline_file,omitempty"`
}

func checkFile(path, model, timeline string) (checkView, error) {
	f, err := os.Open(path)
	if err != nil {
		return checkView{}, err
	}
	defer f.Close()
	ops, err := engine.ReadHistory(f)
	if err != nil {
		return checkView{}, fmt.Errorf("%s: %w", path, err)
	}
	res, err := engine.CheckHistory(model, ops)
	if err != nil {
		return checkView{}, fmt.Errorf("%s: %w", path, err)
	}
	if !res.Checked {
		return checkView{}, fmt.Errorf("%s: no operations to check under model %s", path, model)
	}
	out := checkView{Model: model, Checked: true, Ok: res.Ok, Operations: res.Count, Summary: res.Summary(), Anomalies: res.Anomalies}
	if timeline == "" {
		timeline = strings.TrimSuffix(path, filepath.Ext(path)) + ".html"
	}
	if !res.Ok && res.HasTimeline() && writeFile(timeline, res.Visualize) == nil {
		out.Timeline = timeline
	}
	return out, nil
}

func cmdCheck(args []string) error {
	fs := newFlags("check")
	path := fs.String("history", "", "history file written by run, replay or shrink, for example .seedfall/seed-4.history.jsonl")
	model := fs.String("model", engine.DefaultConfig().Checker.Model, "model to check the history against: "+strings.Join(engine.Models(), ", "))
	timeline := fs.String("timeline", "", "where to write the HTML timeline on a violation (default: the history file with .html)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("-history is required")
	}
	out, err := checkFile(*path, *model, *timeline)
	if err != nil {
		return err
	}
	fmt.Printf("checked %s\n", out.Summary)
	for _, a := range out.Anomalies {
		fmt.Printf("  %s: %s\n", a.Kind, a.Explain)
	}
	if out.Timeline != "" {
		fmt.Printf("linearizability timeline: %s\n", out.Timeline)
	}
	if !out.Ok {
		os.Exit(1)
	}
	return nil
}
