package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kunalgoesbyken/seedfall/gym/grader/internal/gym"
)

const (
	baselinesFile  = "baselines.json"
	cheatsDir      = "cheats"
	exploitsDir    = "exploits"
	solutionPatch  = "fix.patch"
	splitsFile     = "splits.json"
	defaultHeldOut = 0.25
)

type builder struct {
	manifest  gym.Manifest
	patchDir  string
	seedfall  string
	out       string
	cheats    string
	exploits  string
	baselines map[string]gym.Baseline
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "build_tasks:", err)
		os.Exit(1)
	}
}

func run() error {
	manifest := flag.String("manifest", "tasks.json", "declarative task manifest")
	patches := flag.String("patches", "tasks_src", "directory holding the patch files the manifest names")
	src := flag.String("seedfall", "", "seedfall source directory, default SEEDFALL_SRC or the seedfall repository around the working directory")
	out := flag.String("out", "tasks", "output directory for generated tasks")
	cheats := flag.String("cheats", cheatsDir, "output directory for generated cheats")
	exploits := flag.String("exploits", exploitsDir, "directory of exploit sources, each a replacement for the editable paths of the systems that list it")
	baselines := flag.String("baselines", baselinesFile, "calibrated progress floors written by validate -calibrate")
	only := flag.String("only", "", "build only this task id")
	splits := flag.String("splits", splitsFile, "train and held-out split written when every task is built")
	heldOut := flag.Float64("held-out", defaultHeldOut, "fraction of tasks held out, whole system and bug class groups at a time")
	flag.Parse()
	var b builder
	b.patchDir, b.out, b.cheats, b.exploits = *patches, *out, *cheats, *exploits
	var err error
	if b.seedfall, err = gym.SeedfallSource(*src); err != nil {
		return err
	}
	if err := readJSON(*manifest, &b.manifest); err != nil {
		return err
	}
	if err := readJSON(*baselines, &b.baselines); err != nil && !os.IsNotExist(err) {
		return err
	}
	specs, err := b.manifest.Resolve()
	if err != nil {
		return err
	}
	for _, s := range specs {
		if *only != "" && s.ID != *only {
			continue
		}
		if err := b.build(s); err != nil {
			return fmt.Errorf("task %s: %w", s.ID, err)
		}
		fmt.Println("built", s.ID)
	}
	if *only != "" {
		return nil
	}
	return gym.WriteJSON(*splits, gym.AssignSplits(specs, *heldOut))
}

func (b builder) build(s gym.Spec) error {
	sys := b.manifest.Systems[s.System]
	work, err := os.MkdirTemp("", "gym-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	buggy := filepath.Join(work, "buggy")
	if err := b.checkout(sys, buggy); err != nil {
		return err
	}
	if err := b.apply(buggy, s.Patches.Buggy); err != nil {
		return err
	}
	fixed := filepath.Join(work, "fixed")
	if err := copyDir(buggy, fixed); err != nil {
		return err
	}
	if err := b.apply(fixed, s.Patches.Fixed); err != nil {
		return err
	}
	cheat := filepath.Join(work, "cheat")
	if err := copyDir(buggy, cheat); err != nil {
		return err
	}
	if err := b.apply(cheat, s.Patches.Cheat); err != nil {
		return err
	}
	dir := filepath.Join(b.out, s.ID)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := b.writeTask(s, sys, dir, buggy, fixed); err != nil {
		return err
	}
	cheatOut := filepath.Join(b.cheats, s.ID)
	if err := os.RemoveAll(cheatOut); err != nil {
		return err
	}
	if err := copyEditable(sys.Editable, cheat, cheatOut); err != nil {
		return err
	}
	for _, name := range sys.Exploits {
		if err := b.buildExploit(sys, name, filepath.Join(b.cheats, gym.ExploitDir(s.ID, name))); err != nil {
			return fmt.Errorf("exploit %s: %w", name, err)
		}
	}
	return nil
}

func (b builder) buildExploit(sys gym.System, name, out string) error {
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	return copyEditable(sys.Editable, filepath.Join(b.exploits, name), out)
}

func (b builder) writeTask(s gym.Spec, sys gym.System, dir, buggy, fixed string) error {
	if err := copyEditable(sys.Editable, buggy, filepath.Join(dir, gym.WorkspaceDir)); err != nil {
		return err
	}
	if err := copyEditable(sys.Editable, fixed, filepath.Join(dir, gym.SolutionDir)); err != nil {
		return err
	}
	if err := b.writeSolutionPatch(s, filepath.Join(dir, gym.SolutionDir, solutionPatch)); err != nil {
		return err
	}
	harness := filepath.Join(dir, gym.HarnessDir)
	for tree := range sys.Trees {
		if containsTree(sys.Editable, tree) {
			continue
		}
		if err := copyDir(filepath.Join(buggy, tree), filepath.Join(harness, tree)); err != nil {
			return err
		}
	}
	if err := b.writeModule(sys, harness); err != nil {
		return err
	}
	if err := gym.WriteJSON(filepath.Join(harness, gym.ConfigFile), gym.MergeConfig(sys.Config, s.Config)); err != nil {
		return err
	}
	if err := gym.WriteJSON(filepath.Join(harness, gym.BaselineFile), b.baselines[s.ID]); err != nil {
		return err
	}
	task := gym.Task{ID: s.ID, System: s.System, BugClass: s.BugClass, Difficulty: s.Difficulty, DefaultN: s.N, Editable: sys.Editable, Exploits: sys.Exploits, Symptom: s.Symptom}
	if err := gym.WriteJSON(filepath.Join(dir, gym.TaskFile), task); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, gym.SeedFile), fmt.Appendf(nil, "%d\n", *s.VisibleSeed), 0o644)
}

func (b builder) writeSolutionPatch(s gym.Spec, path string) error {
	var all []byte
	for _, p := range s.Patches.Fixed {
		data, err := os.ReadFile(filepath.Join(b.patchDir, p))
		if err != nil {
			return err
		}
		all = append(all, data...)
	}
	return os.WriteFile(path, all, 0o644)
}

func (b builder) writeModule(sys gym.System, harness string) error {
	mod := "module gymtask\n\ngo 1.25.0\n\nrequire github.com/kunalgoesbyken/seedfall v0.0.0\n"
	if sys.ModuleFrom != "" {
		data, err := os.ReadFile(filepath.Join(b.seedfall, sys.ModuleFrom, gym.ModFile))
		if err != nil {
			return err
		}
		mod = stripReplace(string(data))
		sum, err := os.ReadFile(filepath.Join(b.seedfall, sys.ModuleFrom, "go.sum"))
		if err == nil {
			if err := os.WriteFile(filepath.Join(harness, "go.sum"), sum, 0o644); err != nil {
				return err
			}
		}
	}
	return os.WriteFile(filepath.Join(harness, gym.ModFile), []byte(mod), 0o644)
}

func stripReplace(mod string) string {
	var kept []string
	for _, line := range strings.Split(mod, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "replace ") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func (b builder) checkout(sys gym.System, dir string) error {
	for tree, from := range sys.Trees {
		if err := copyDir(filepath.Join(b.seedfall, filepath.FromSlash(from)), filepath.Join(dir, tree)); err != nil {
			return err
		}
	}
	return nil
}

func (b builder) apply(dir string, patches []string) error {
	for _, p := range patches {
		abs, err := filepath.Abs(filepath.Join(b.patchDir, p))
		if err != nil {
			return err
		}
		cmd := exec.Command("git", "apply", "-p1", "--whitespace=nowarn", abs)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("apply %s: %w: %s", p, err, out)
		}
	}
	return nil
}

func containsTree(editable []string, tree string) bool {
	for _, e := range editable {
		if filepath.Clean(e) == filepath.Clean(tree) {
			return true
		}
	}
	return false
}

func copyEditable(editable []string, from, to string) error {
	for _, e := range editable {
		if err := copyDir(filepath.Join(from, e), filepath.Join(to, e)); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
