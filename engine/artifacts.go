package engine

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	ArtifactReport   = "report.json"
	ArtifactTrace    = "trace.txt"
	ArtifactFaults   = "faults.json"
	ArtifactHistory  = "history.jsonl"
	ArtifactTimeline = "linearizability.html"
	ArtifactRun      = "timeline.html"
	ArtifactMermaid  = "timeline.mmd"
	ArtifactDisks    = "disk"
)

type ArtifactOptions struct {
	Config       string
	ScheduleFile string
	Label        string
	Original     *Result
}

type Artifact struct {
	Dir            string         `json:"-"`
	Seed           uint64         `json:"seed"`
	Reproduced     bool           `json:"reproduced"`
	Failure        Failure        `json:"failure"`
	Finding        string         `json:"finding"`
	Reason         string         `json:"reason"`
	At             string         `json:"at"`
	Steps          int            `json:"steps"`
	HostCalls      int64          `json:"host_calls"`
	Hash           string         `json:"determinism_hash"`
	Classification Classification `json:"classification"`
	ScheduleFile   string         `json:"schedule_file,omitempty"`
	Config         string         `json:"config"`
	ConfigSHA256   string         `json:"config_sha256"`
	Version        string         `json:"seedfall_version"`
	GoVersion      string         `json:"go_version"`
	Commit         string         `json:"commit,omitempty"`
	Created        time.Time      `json:"created"`
	Replay         string         `json:"replay"`
	Files          []string       `json:"files"`
}

func ArtifactDir(cfg *Config, seed uint64, label string) string {
	name := fmt.Sprintf("%s-seed-%d", cfg.Stem(), seed)
	if label != "" {
		name += "-" + label
	}
	return filepath.Join(cfg.Dir, ".seedfall", "failures", name)
}

func (r *Runner) SaveArtifacts(seed uint64, s Schedule, opts ArtifactOptions) (Artifact, Result, error) {
	in, err := r.Inspect(seed, s, InspectOptions{Timeline: true})
	if err != nil {
		return Artifact{}, Result{}, err
	}
	res := r.doublecheck(seed, s, in.Result)
	original := cmp.Or(opts.Original, &res)
	class := r.Classify(*original, s)
	dir := ArtifactDir(r.cfg, seed, opts.Label)
	a := Artifact{
		Dir: dir, Seed: seed, Reproduced: res.sameFailure(*original), Classification: class, Failure: res.Failure, Finding: res.Failure.Key(),
		Reason: res.Reason, At: res.At.String(), Steps: res.Steps, HostCalls: res.HostCalls, Hash: res.Hash(),
		ScheduleFile: opts.ScheduleFile, Config: opts.Config, Version: Version(), GoVersion: runtime.Version(),
		Commit: Commit(r.cfg.Dir), Created: time.Now().UTC(), Replay: replayCommand(opts.Config, seed, dir),
	}
	a.ConfigSHA256 = r.cfg.Digest()
	if err := os.RemoveAll(a.Dir); err != nil {
		return a, res, err
	}
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return a, res, err
	}
	return a, res, a.write(res, in.Result.Timeline, s, in.Disks, fmt.Sprintf("%s seed %d", r.cfg.Stem(), seed))
}

func replayCommand(config string, seed uint64, dir string) string {
	return fmt.Sprintf("seedfall replay -c %s -seed %d -faults %s", config, seed, filepath.ToSlash(filepath.Join(dir, ArtifactFaults)))
}

func (a *Artifact) write(res Result, tl *Timeline, s Schedule, disks []Disk, title string) error {
	writers := []struct {
		name  string
		write func(io.Writer) error
		keep  bool
	}{
		{ArtifactTrace, func(w io.Writer) error { _, err := io.WriteString(w, strings.Join(res.Trace, "\n")+"\n"); return err }, true},
		{ArtifactFaults, func(w io.Writer) error { return encodeJSON(w, ScheduleFile{Seed: a.Seed, Schedule: res.Schedule(s)}) }, true},
		{ArtifactHistory, func(w io.Writer) error { return WriteHistory(w, res.History.Ops) }, len(res.History.Ops) > 0},
		{ArtifactTimeline, res.History.Visualize, res.History.Checked && !res.History.Ok && res.History.HasTimeline()},
		{ArtifactRun, func(w io.Writer) error { return tl.WriteHTML(w, title) }, tl != nil},
		{ArtifactMermaid, func(w io.Writer) error { return tl.WriteMermaid(w) }, tl != nil},
	}
	for _, f := range writers {
		if !f.keep {
			continue
		}
		if err := writeArtifact(filepath.Join(a.Dir, f.name), f.write); err != nil {
			return err
		}
		a.Files = append(a.Files, f.name)
	}
	roots, err := ExportDisks(disks, filepath.Join(a.Dir, ArtifactDisks))
	for _, root := range roots {
		rel, _ := filepath.Rel(a.Dir, root)
		a.Files = append(a.Files, filepath.ToSlash(rel)+"/")
	}
	if err != nil {
		return err
	}
	a.Files = append(a.Files, ArtifactReport)
	return writeArtifact(filepath.Join(a.Dir, ArtifactReport), func(w io.Writer) error { return encodeJSON(w, a) })
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeArtifact(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
