package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
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
