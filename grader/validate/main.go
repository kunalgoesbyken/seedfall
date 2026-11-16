package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kunalgoesbyken/seedfall-gym/grader/internal/gym"
)

const (
	calibrationSalt     = "calibration"
	calibrationSeeds    = 128
	defaultFloorDivisor = 2
)

var (
	jobs         int
	floorDivisor int
)

type row struct {
	Task        string  `json:"task"`
	Kept        bool    `json:"kept"`
	Reason      string  `json:"reason,omitempty"`
	BuggyPass   float64 `json:"buggy_pass_rate"`
	BuggyReward float64 `json:"buggy_reward"`
	Oracle      float64 `json:"oracle_reward"`
	Nop         bool    `json:"nop_matches_buggy"`
	Cheat       float64 `json:"cheat_reward"`
	Seconds     float64 `json:"seconds_per_grade"`
	MinAcks     int     `json:"min_acks"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "validate:", err)
		os.Exit(1)
	}
}

func run() error {
	tasksDir := flag.String("tasks", "tasks", "generated tasks directory")
	cheatsDir := flag.String("cheats", "cheats", "generated cheats directory")
	n := flag.Int("n", 0, "hidden seeds per grade, default the task default")
	only := flag.String("only", "", "comma separated task ids")
	calibrate := flag.Bool("calibrate", false, "measure the oracle's acknowledged operations and write the progress floors")
	baselines := flag.String("baselines", "baselines.json", "progress floors file written by -calibrate")
	report := flag.String("report", "validation.json", "write the validation table as JSON")
	src := flag.String("seedfall", "", "seedfall source directory, default SEEDFALL_SRC or the module the grader links")
	flag.IntVar(&jobs, "j", 0, "parallel runs per grade, default all CPUs")
	flag.IntVar(&floorDivisor, "floor-divisor", defaultFloorDivisor, "with -calibrate, the progress floor is the lowest oracle ack count divided by this")
	saltEnv := flag.String("salt-env", "SEEDFALL_GYM_SALT", "environment variable holding the salt, the dev salt is used when it is empty")
	flag.Parse()
	source, err := gym.SeedfallSource(*src)
	if err != nil {
		return err
	}
	salt, err := gym.Salt(*saltEnv, true)
	if err != nil {
		return err
	}
	tasks, err := gym.ListTasks(*tasksDir)
	if err != nil {
		return err
	}
	if *only != "" {
		ids := strings.Split(*only, ",")
		tasks = slices.DeleteFunc(tasks, func(t gym.Task) bool { return !slices.Contains(ids, t.ID) })
	}
	if *calibrate {
		return calibrateAll(tasks, source, *baselines)
	}
	var rows []row
	for _, t := range tasks {
		r, err := validate(t, *cheatsDir, source, salt, t.Seeds(*n))
		if err != nil {
			return fmt.Errorf("task %s: %w", t.ID, err)
		}
		rows = append(rows, r)
		fmt.Fprintf(os.Stderr, "validated %s kept=%t %s\n", r.Task, r.Kept, r.Reason)
	}
	printTable(rows)
	return gym.WriteJSON(*report, rows)
}

func grade(t gym.Task, workspace, source string, seeds []uint64) (gym.Report, float64, error) {
	started := time.Now()
	rep, err := gym.Grade(gym.Options{Task: t, Workspace: workspace, SeedfallSrc: source, Seeds: seeds, Jobs: jobs})
	return rep, time.Since(started).Seconds(), err
}

func validate(t gym.Task, cheats, source string, salt []byte, n int) (row, error) {
	seeds := gym.HiddenSeeds(salt, t.ID, n)
	base, err := t.Baseline()
	if err != nil {
		return row{}, err
	}
	r := row{Task: t.ID, MinAcks: base.MinAcks}
	buggy, secs, err := grade(t, t.Workspace(), source, seeds)
	if err != nil {
		return r, err
	}
	r.Seconds, r.BuggyReward, r.BuggyPass = secs, buggy.Reward, float64(buggy.Passed)/float64(n)
	oracle, _, err := grade(t, t.Solution(), source, seeds)
	if err != nil {
		return r, err
	}
	r.Oracle = oracle.Reward
	nop, err := nopGrade(t, source, seeds)
	if err != nil {
		return r, err
	}
	r.Nop = slices.Equal(hashes(buggy), hashes(nop))
	cheat, _, err := grade(t, filepath.Join(cheats, t.ID), source, seeds)
	if err != nil {
		return r, err
	}
	r.Cheat = cheat.Reward
	r.Reason = verdict(r, buggy, oracle, cheat)
	r.Kept = r.Reason == ""
	return r, nil
}

func nopGrade(t gym.Task, source string, seeds []uint64) (gym.Report, error) {
	copyDir, err := os.MkdirTemp("", "gym-nop-")
	if err != nil {
		return gym.Report{}, err
	}
	defer os.RemoveAll(copyDir)
	if err := os.CopyFS(copyDir, os.DirFS(t.Workspace())); err != nil {
		return gym.Report{}, err
	}
	rep, _, err := grade(t, copyDir, source, seeds)
	return rep, err
}

func verdict(r row, buggy, oracle, cheat gym.Report) string {
	var why []string
	if !buggy.BuildOK {
		why = append(why, "buggy workspace does not build")
	}
	if r.BuggyPass >= 0.5 {
		why = append(why, fmt.Sprintf("buggy passes %.0f%% of hidden seeds", r.BuggyPass*100))
	}
	if !oracle.BuildOK || oracle.Reward != 1 {
		why = append(why, fmt.Sprintf("oracle reward %.3f (guard tripped %t)", oracle.Reward, oracle.Guard.Tripped))
	}
	if !r.Nop {
		why = append(why, "nop run differs from buggy run")
	}
	if cheat.Reward != 0 {
		why = append(why, fmt.Sprintf("cheat reward %.3f", cheat.Reward))
	}
	return strings.Join(why, "; ")
}

func hashes(rep gym.Report) []string {
	var out []string
	for _, s := range rep.PerSeed {
		out = append(out, s.Hash)
	}
	return out
}

func printTable(rows []row) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "task\tbuggy pass\tbuggy reward\toracle\tcheat\tnop==buggy\tsec/grade\tfloor\tkept\treason")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%.2f\t%.3f\t%.3f\t%.3f\t%t\t%.1f\t%d\t%t\t%s\n", r.Task, r.BuggyPass, r.BuggyReward, r.Oracle, r.Cheat, r.Nop, r.Seconds, r.MinAcks, r.Kept, r.Reason)
	}
	w.Flush()
}

func calibrateAll(tasks []gym.Task, source, path string) error {
	floors := map[string]gym.Baseline{}
	if err := readFloors(path, floors); err != nil {
		return err
	}
	for _, t := range tasks {
		seeds := gym.HiddenSeeds([]byte(calibrationSalt), t.ID, calibrationSeeds)
		rep, _, err := grade(t, t.Solution(), source, seeds)
		if err != nil {
			return err
		}
		if !rep.BuildOK {
			return fmt.Errorf("task %s: oracle does not build: %s", t.ID, rep.BuildError)
		}
		lowest := rep.PerSeed[0].Acks
		for _, s := range rep.PerSeed {
			lowest = min(lowest, s.Acks)
		}
		floors[t.ID] = gym.Baseline{MinAcks: max(1, lowest/floorDivisor), Seeds: calibrationSeeds}
		fmt.Printf("%s lowest oracle acks %d, floor %d\n", t.ID, lowest, floors[t.ID].MinAcks)
	}
	return gym.WriteJSON(path, floors)
}

func readFloors(path string, into map[string]gym.Baseline) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &into)
}
