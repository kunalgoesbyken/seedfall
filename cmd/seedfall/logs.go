package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

type logFilter struct {
	Nodes []string
	Grep  *regexp.Regexp
	From  time.Duration
	To    time.Duration
}

func newLogFilter(nodes, grep, from, to string) (logFilter, error) {
	f := logFilter{To: -1}
	if nodes != "" {
		f.Nodes = strings.Split(nodes, ",")
	}
	var err error
	if grep != "" {
		if f.Grep, err = regexp.Compile(grep); err != nil {
			return f, err
		}
	}
	if f.From, err = parseAt(from); err != nil {
		return f, err
	}
	if to != "" {
		if f.To, err = time.ParseDuration(to); err != nil {
			return f, err
		}
	}
	return f, nil
}

func (f logFilter) keep(l engine.TraceLine) bool {
	return (len(f.Nodes) == 0 || slices.Contains(f.Nodes, l.Node)) &&
		l.At >= f.From && (f.To < 0 || l.At <= f.To) &&
		(f.Grep == nil || f.Grep.MatchString(l.Text))
}

func (f logFilter) apply(lines []engine.TraceLine) []engine.TraceLine {
	return slices.DeleteFunc(slices.Clone(lines), func(l engine.TraceLine) bool { return !f.keep(l) })
}

type logView struct {
	At       string         `json:"at"`
	AtNanos  int64          `json:"at_ns"`
	Node     string         `json:"node,omitempty"`
	Instance string         `json:"instance,omitempty"`
	Line     string         `json:"line"`
	Output   string         `json:"output,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}

func viewLog(l engine.TraceLine) logView {
	v := logView{At: l.At.String(), AtNanos: int64(l.At), Node: l.Node, Instance: l.Instance, Line: l.Text, Output: l.Output}
	if strings.HasPrefix(strings.TrimSpace(l.Output), "{") && json.Unmarshal([]byte(l.Output), &v.Fields) != nil {
		v.Fields = nil
	}
	return v
}

func recordedRun(ca *configArgs, seed uint64, faultsFile string, netTrace bool) (engine.Result, *engine.Config, error) {
	r, cfg, err := load(ca.ref())
	if err != nil {
		return engine.Result{}, nil, err
	}
	sched, err := readSchedule(faultsFile)
	if err != nil {
		return engine.Result{}, nil, err
	}
	r.NetTrace = netTrace
	return r.Record(seed, sched), cfg, nil
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	ca := configFlags(fs)
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "fault schedule from seedfall shrink")
	node := fs.String("node", "", "only lines of these nodes, comma separated: their output, starts, crashes and faults")
	grep := fs.String("grep", "", "only lines matching this regular expression")
	from := fs.String("from", "", "only lines at or after this virtual time, for example 1.5s")
	to := fs.String("to", "", "only lines at or before this virtual time")
	jsonOut := fs.Bool("json", false, "print one JSON object per line, with slog JSON output parsed into fields")
	netTrace := fs.Bool("net-trace", false, "also trace every connection, message sent and message read")
	fs.Parse(args)
	filter, err := newLogFilter(*node, *grep, *from, *to)
	if err != nil {
		return err
	}
	res, _, err := recordedRun(ca, *seed, *faultsFile, *netTrace)
	if err != nil {
		return err
	}
	if unknown := filter.unknownNodes(res.Timeline.Nodes); unknown != "" {
		return fmt.Errorf("unknown node %q", unknown)
	}
	lines := filter.apply(res.Timeline.Logs)
	enc := json.NewEncoder(os.Stdout)
	for _, l := range lines {
		if *jsonOut {
			if err := enc.Encode(viewLog(l)); err != nil {
				return err
			}
			continue
		}
		fmt.Println(l.Text)
	}
	fmt.Fprintf(os.Stderr, "%d of %d trace lines; seed %d: %s\n", len(lines), len(res.Timeline.Logs), *seed, status(res))
	return nil
}

func (f logFilter) unknownNodes(nodes []string) string {
	for _, n := range f.Nodes {
		if !slices.Contains(nodes, n) {
			return n
		}
	}
	return ""
}

func writeTimeline(path string, res engine.Result, title string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".mmd") {
		return writeFile(path, res.Timeline.WriteMermaid)
	}
	return writeFile(path, func(w io.Writer) error { return res.Timeline.WriteHTML(w, title) })
}

func timelineTitle(cfg *engine.Config, seed uint64) string {
	return fmt.Sprintf("%s seed %d", cfg.Stem(), seed)
}

func cmdTrace(args []string) error {
	if len(args) == 0 || args[0] != "diff" {
		return errors.New("usage: seedfall trace diff <trace.txt or artifacts dir> <trace.txt or artifacts dir>")
	}
	fs := flag.NewFlagSet("trace diff", flag.ExitOnError)
	around := fs.Int("context", 3, "matching lines to show before the first difference")
	fs.Parse(args[1:])
	if fs.NArg() != 2 {
		return errors.New("usage: seedfall trace diff <trace.txt or artifacts dir> <trace.txt or artifacts dir>")
	}
	a, err := readTrace(fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := readTrace(fs.Arg(1))
	if err != nil {
		return err
	}
	line, diverged := engine.FirstDivergence(a, b)
	if !diverged {
		fmt.Printf("the traces are identical, %d lines\n", len(a))
		return nil
	}
	fmt.Printf("the traces diverge at line %d (%s has %d lines, %s has %d)\n\n", line+1, fs.Arg(0), len(a), fs.Arg(1), len(b))
	for i := max(0, line-*around); i < line; i++ {
		fmt.Printf("  %s\n", a[i])
	}
	fmt.Printf("- %s\n+ %s\n", engine.LineAt(a, line), engine.LineAt(b, line))
	os.Exit(1)
	return nil
}

func readTrace(path string) ([]string, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, engine.ArtifactTrace)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}
