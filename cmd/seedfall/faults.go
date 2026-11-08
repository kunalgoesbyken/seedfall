package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type faultShift struct {
	Fault int    `json:"fault" jsonschema:"position of the fault in the schedule, counting from 1"`
	By    string `json:"by" jsonschema:"how far to move it: a signed duration such as -50ms for a timed fault, or a signed count such as +2 for a fault that fires at a disk operation, read or buggify hit"`
}

type faultsEditArgs struct {
	configArgs
	Seed   uint64         `json:"seed" jsonschema:"seed the schedule belongs to"`
	Faults string         `json:"faults,omitempty" jsonschema:"schedule file to edit; default .seedfall/seed-N.faults.json next to the config, or the seed's own faults when that file does not exist"`
	Remove []int          `json:"remove,omitempty" jsonschema:"positions of faults to remove, counting from 1"`
	Shift  []faultShift   `json:"shift,omitempty" jsonschema:"faults to move earlier or later"`
	Add    []engine.Fault `json:"add,omitempty" jsonschema:"faults to add, in the faults JSON format of docs/faults.md"`
	Out    string         `json:"out,omitempty" jsonschema:"write the edited schedule to this file instead of the one it came from"`
}

type faultEntry struct {
	N     int          `json:"n"`
	Text  string       `json:"text"`
	Fault engine.Fault `json:"fault"`
}

type faultsView struct {
	File     string       `json:"file"`
	From     string       `json:"from"`
	Wrote    bool         `json:"wrote"`
	Duration string       `json:"duration,omitempty"`
	Ops      int64        `json:"ops,omitempty"`
	Faults   []faultEntry `json:"faults"`
	Replay   string       `json:"replay_command"`
}

func cmdFaults(args []string) error {
	fs := newFlags("faults")
	var a faultsEditArgs
	a.register(fs)
	seed := seedFlag(fs)
	fs.StringVar(&a.Faults, "faults", "", "schedule file to edit (default: .seedfall/seed-N.faults.json, or the seed's own faults when it does not exist)")
	fs.StringVar(&a.Out, "out", "", "write the edited schedule here instead of the file it came from")
	fs.Func("remove", "position of a fault to remove, counting from 1; repeatable", func(s string) error {
		n, err := strconv.Atoi(s)
		a.Remove = append(a.Remove, n)
		return err
	})
	fs.Func("shift", "move a fault, as position=amount: 2=-50ms for a timed fault, 3=+2 for a disk-op, read or buggify fault; repeatable", func(s string) error {
		pos, by, ok := strings.Cut(s, "=")
		n, err := strconv.Atoi(pos)
		if !ok || err != nil {
			return fmt.Errorf("want position=amount, got %q", s)
		}
		a.Shift = append(a.Shift, faultShift{Fault: n, By: by})
		return nil
	})
	fs.Func("add", `fault to add as JSON, for example {"at":"1s","kind":"crash","node":"server","until":"1.2s"}; repeatable`, func(s string) error {
		var f engine.Fault
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			return err
		}
		a.Add = append(a.Add, f)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return err
	}
	a.Seed = *seed
	out, err := editFaults(a)
	if err != nil {
		return err
	}
	written := "unchanged"
	if out.Wrote {
		written = "wrote " + out.File
	}
	fmt.Printf("seed %d, %d faults from %s, %s\n", a.Seed, len(out.Faults), out.From, written)
	for _, f := range out.Faults {
		fmt.Printf("%3d %s\n", f.N, f.Text)
	}
	if out.Duration != "" {
		fmt.Printf("duration %s\n", out.Duration)
	}
	fmt.Printf("replay: %s\n", out.Replay)
	return nil
}

func toolFaultsEdit(_ context.Context, _ *mcp.CallToolRequest, a faultsEditArgs) (*mcp.CallToolResult, faultsView, error) {
	out, err := editFaults(a)
	return nil, out, err
}

func editFaults(a faultsEditArgs) (faultsView, error) {
	cfg, err := engine.Load(a.ref())
	if err != nil {
		return faultsView{}, err
	}
	file, from, err := scheduleSource(a.Config, a.Seed, a.Faults, func() (engine.Schedule, error) {
		r, _, err := load(a.ref())
		if err != nil {
			return engine.Schedule{}, err
		}
		return r.Run(a.Seed, nil).Schedule(engine.Schedule{}), nil
	})
	if err != nil {
		return faultsView{}, err
	}
	source := cmp.Or(a.Faults, schedulePath(a.Config, a.Seed))
	generated := from != filepath.ToSlash(source)
	edited := len(a.Remove)+len(a.Shift)+len(a.Add) > 0
	if file.Faults, err = applyEdits(file.Faults, a); err != nil {
		return faultsView{}, err
	}
	if edited {
		file.ShrunkFrom, file.ShrinkRuns, file.BudgetExhausted = nil, 0, false
	}
	if err := cfg.CheckSchedule(file.Schedule); err != nil {
		return faultsView{}, err
	}
	path := cmp.Or(a.Out, source)
	out := faultsView{File: filepath.ToSlash(path), From: from, Ops: file.Ops, Faults: []faultEntry{}}
	out.Replay = fmt.Sprintf("seedfall replay -c %s -seed %d -faults %s", a.ref().String(), a.Seed, out.File)
	if file.Duration > 0 {
		out.Duration = time.Duration(file.Duration).String()
	}
	for i, f := range file.Faults {
		out.Faults = append(out.Faults, faultEntry{N: i + 1, Text: f.Describe(), Fault: f})
	}
	if !edited && a.Out == "" && !generated {
		return out, nil
	}
	out.Wrote = true
	return out, engine.WriteScheduleFile(path, file)
}

func applyEdits(faults []engine.Fault, a faultsEditArgs) ([]engine.Fault, error) {
	out := slices.Clone(faults)
	position := func(n int) error {
		if n < 1 || n > len(faults) {
			return fmt.Errorf("no fault %d: the schedule has %d, counting from 1", n, len(faults))
		}
		return nil
	}
	for _, s := range a.Shift {
		if err := position(s.Fault); err != nil {
			return nil, err
		}
		moved, err := shiftFault(out[s.Fault-1], s.By)
		if err != nil {
			return nil, fmt.Errorf("fault %d: %w", s.Fault, err)
		}
		out[s.Fault-1] = moved
	}
	removed := map[int]bool{}
	for _, n := range a.Remove {
		if err := position(n); err != nil {
			return nil, err
		}
		removed[n-1] = true
	}
	kept := []engine.Fault{}
	for i, f := range out {
		if !removed[i] {
			kept = append(kept, f)
		}
	}
	kept = append(kept, a.Add...)
	slices.SortStableFunc(kept, func(x, y engine.Fault) int { return cmp.Compare(x.At, y.At) })
	return kept, nil
}

func shiftFault(f engine.Fault, by string) (engine.Fault, error) {
	if f.FiresAtOp() {
		n, err := strconv.ParseInt(by, 10, 64)
		if err != nil {
			return f, fmt.Errorf("a %s fault fires at a disk operation, read or buggify hit, so shift it by a count such as +2, not %q", f.Kind, by)
		}
		f.Op += n
		return f, nil
	}
	d, err := time.ParseDuration(by)
	if err != nil {
		return f, fmt.Errorf("a %s fault fires at a virtual time, so shift it by a duration such as -50ms, not %q", f.Kind, by)
	}
	if f.Until >= f.At {
		f.Until += engine.Duration(d)
	}
	f.At += engine.Duration(d)
	return f, nil
}
