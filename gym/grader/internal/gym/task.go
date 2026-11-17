package gym

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	WorkspaceDir = "workspace"
	HarnessDir   = "harness"
	SolutionDir  = "solution"
	TaskFile     = "task.json"
	SeedFile     = "visible_seed"
	ConfigFile   = "seedfall.json"
	BaselineFile = "baseline.json"
	ModFile      = "go.mod"
	AckEvent     = "gym.ack"
	DefaultSeeds = 64
)

type Task struct {
	ID         string   `json:"id"`
	System     string   `json:"system"`
	BugClass   string   `json:"bug_class"`
	Difficulty string   `json:"difficulty"`
	Editable   []string `json:"editable"`
	Exploits   []string `json:"exploits,omitempty"`
	Symptom    string   `json:"symptom"`
	DefaultN   int      `json:"default_n"`
	Dir        string   `json:"-"`
}

type Baseline struct {
	MinAcks   int `json:"min_acks"`
	GuardAcks int `json:"guard_acks"`
	Seeds     int `json:"calibration_seeds"`
}

func LoadTask(dir string) (Task, error) {
	var t Task
	if err := readJSON(filepath.Join(dir, TaskFile), &t); err != nil {
		return t, err
	}
	t.Dir = dir
	return t, nil
}

func (t Task) Seeds(n int) int {
	if n > 0 {
		return n
	}
	if t.DefaultN > 0 {
		return t.DefaultN
	}
	return DefaultSeeds
}

func ExploitDir(id, exploit string) string { return id + "." + exploit }

func (t Task) Harness() string { return filepath.Join(t.Dir, HarnessDir) }

func (t Task) Workspace() string { return filepath.Join(t.Dir, WorkspaceDir) }

func (t Task) Solution() string { return filepath.Join(t.Dir, SolutionDir) }

func (t Task) Baseline() (Baseline, error) {
	var b Baseline
	err := readJSON(filepath.Join(t.Harness(), BaselineFile), &b)
	return b, err
}

func (t Task) VisibleSeed() (uint64, error) {
	b, err := os.ReadFile(filepath.Join(t.Dir, SeedFile))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

func ListTasks(root string) ([]Task, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var tasks []Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := LoadTask(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", e.Name(), err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
