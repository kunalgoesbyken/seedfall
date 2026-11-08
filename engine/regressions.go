package engine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const RegressionsDir = "seedfall-regressions"

type Regression struct {
	Source string
	Path   string
	Label  string
	File   ScheduleFile
}

type RegressionResult struct {
	Regression
	Result Result
}

func (r Regression) String() string { return fmt.Sprintf("seed %d from %s", r.File.Seed, r.Source) }

func (r Regression) Schedule() Schedule {
	s := r.File.Schedule
	s.Duration = 0
	return s
}

func PinPath(cfg *Config, seed uint64) string {
	stem := strings.TrimSuffix(filepath.Base(cfg.Path), filepath.Ext(cfg.Path))
	return filepath.Join(cfg.Dir, RegressionsDir, fmt.Sprintf("%s-seed-%d.json", stem, seed))
}

func Pinned(cfg *Config) ([]Regression, error) {
	paths, err := filepath.Glob(filepath.Join(cfg.Dir, RegressionsDir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Regression
	for _, p := range paths {
		file, err := ReadScheduleFile(p)
		if err != nil {
			return nil, err
		}
		if file.Pinned != nil && file.Pinned.Config != "" && file.Pinned.Config != filepath.Base(cfg.Path) {
			continue
		}
		out = append(out, Regression{Source: p, Path: p, Label: "pinned", File: file})
	}
	return out, nil
}

func SeedsFile(path string) ([]Regression, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Regression
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text, _, _ := strings.Cut(scanner.Text(), "#")
		for _, field := range strings.Fields(text) {
			seed, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %q is not a seed", path, line, field)
			}
			out = append(out, Regression{Source: fmt.Sprintf("%s:%d", path, line), File: ScheduleFile{Seed: seed}})
		}
	}
	return out, scanner.Err()
}

func ReplayRegressions(r *Runner, regs []Regression, jobs int) []RegressionResult {
	out := make([]RegressionResult, len(regs))
	slots := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, reg := range regs {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			out[i] = RegressionResult{Regression: reg, Result: r.Replay(reg.File.Seed, reg.Schedule())}
		})
	}
	wg.Wait()
	return out
}

func FirstFailed(results []RegressionResult) *RegressionResult {
	for i := range results {
		if results[i].Result.Failed {
			return &results[i]
		}
	}
	return nil
}

func (r Regression) ArtifactOptions(config string, original *Result) ArtifactOptions {
	return ArtifactOptions{Config: config, ScheduleFile: r.Path, Label: r.Label, Original: original}
}
