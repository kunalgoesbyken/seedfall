package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

var (
	FaultKinds  = []string{FaultCrash, FaultCrashIO, FaultPartition, FaultPause, FaultClockJump, FaultClog, FaultReset, FaultSlow, FaultBitrot, FaultFsyncEIO, FaultWriteEIO, FaultENOSPC, FaultReadEIO, FaultReadCorrupt, FaultLostWrite, FaultMisdirected, FaultBuggify, FaultPreempt, FaultUDPDrop, FaultUDPDup}
	windowKinds = []string{FaultCrash, FaultCrashIO, FaultPartition, FaultPause, FaultClog, FaultSlow}
)

type ScheduleFile struct {
	Seed uint64 `json:"seed"`
	Schedule
	ShrunkFrom      *ScheduleSize `json:"shrunk_from,omitempty"`
	ShrinkRuns      int           `json:"shrink_runs,omitempty"`
	BudgetExhausted bool          `json:"budget_exhausted,omitempty"`
	Pinned          *Pin          `json:"pinned,omitempty"`
}

type Pin struct {
	Config  string    `json:"config"`
	Profile string    `json:"profile,omitempty"`
	Set     []string  `json:"set,omitempty"`
	Finding string    `json:"finding,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	From    string    `json:"from,omitempty"`
	Version string    `json:"seedfall_version"`
	Date    time.Time `json:"date"`
}

func ReadScheduleFile(path string) (ScheduleFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ScheduleFile{}, err
	}
	var file ScheduleFile
	if trimmed := bytes.TrimSpace(b); len(trimmed) > 0 && trimmed[0] == '[' {
		err = json.Unmarshal(trimmed, &file.Faults)
	} else {
		err = json.Unmarshal(b, &file)
	}
	if err != nil {
		return ScheduleFile{}, fmt.Errorf("%s: %w", path, err)
	}
	if file.Faults == nil {
		file.Faults = []Fault{}
	}
	return file, nil
}

func WriteScheduleFile(path string, file ScheduleFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (r Result) Schedule(s Schedule) Schedule {
	faults := r.Faults
	if faults == nil {
		faults = []Fault{}
	}
	return Schedule{Faults: faults, Duration: Duration(r.Horizon), Ops: s.Ops}
}

func (f Fault) FiresAtOp() bool { return observedAtRuntime(f.Kind) }

func (c *Config) CheckSchedule(s Schedule) error {
	horizon := time.Duration(c.Duration)
	if s.Duration > 0 {
		horizon = time.Duration(s.Duration)
	}
	names := namesWhere(c, func(NodeSpec) bool { return true })
	var errs []error
	for i, f := range s.Faults {
		problems := f.problems(names, horizon)
		if f.Kind == FaultPreempt && c.Preempt.Strategy != StrategyPCT {
			problems = append(problems, fmt.Sprintf("a preempt fault fires only with preempt.strategy %s, and the config's strategy is %s", StrategyPCT, c.Preempt.Strategy))
		}
		for _, problem := range problems {
			errs = append(errs, fmt.Errorf("fault %d (%s at %s): %s", i+1, f.Kind, time.Duration(f.At), problem))
		}
	}
	return errors.Join(errs...)
}

func (f Fault) problems(names []string, horizon time.Duration) []string {
	if !slices.Contains(FaultKinds, f.Kind) {
		return []string{fmt.Sprintf("unknown kind, want one of %v", FaultKinds)}
	}
	var out []string
	add := func(bad bool, format string, args ...any) {
		if bad {
			out = append(out, fmt.Sprintf(format, args...))
		}
	}
	add(f.At < 0, "at is negative")
	add(!f.FiresAtOp() && time.Duration(f.At) > horizon, "at is after the end of the run at %s, so it never fires", horizon)
	add(slices.Contains(windowKinds, f.Kind) && f.Until < f.At, "until %s is before at", time.Duration(f.Until))
	for _, n := range append(append([]string{f.Node, f.From, f.To}, f.Side...), slices.Concat(f.Groups...)...) {
		add(n != "" && !slices.Contains(names, n), "unknown node %q, want one of %v", n, names)
	}
	switch f.Kind {
	case FaultPartition:
		add((len(f.Side) == 0) == (len(f.Groups) == 0), "a partition needs either side or groups")
	case FaultClog:
		add(!slices.Contains(clogModes, f.Mode), "mode %q, want one of %v", f.Mode, clogModes)
		add(f.Dir != "" && !slices.Contains(clogDirs, f.Dir), "dir %q, want one of %v, or none for a from and to link", f.Dir, clogDirs)
		add(f.Dir != "" && f.Node == "", "a clog with dir needs node")
		add(f.Dir == "" && (f.From == "" || f.To == "" || f.From == f.To), "a clog without dir needs two different nodes in from and to")
	default:
		add(f.Node == "", "node is required")
	}
	add(f.Kind == FaultSlow && f.Factor < 1, "factor %g is below 1", f.Factor)
	add(f.FiresAtOp() && f.Op < 1, "op %d is below 1: disk operations, reads, buggify hits, counted calls and udp datagrams count from 1", f.Op)
	add(f.Kind == FaultBuggify && f.Site == "", "a buggify fault needs site")
	return out
}
