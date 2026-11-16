package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kunalgoesbyken/seedfall-gym/grader/internal/gym"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "grade:", err)
		os.Exit(1)
	}
}

func run() error {
	taskDir := flag.String("task", "", "task directory, tasks/<id>")
	workspace := flag.String("workspace", "", "agent workspace directory")
	n := flag.Int("n", 0, "number of hidden seeds, default the task default")
	saltEnv := flag.String("salt-env", "SEEDFALL_GYM_SALT", "environment variable holding the secret salt")
	dev := flag.Bool("dev", false, "use the fixed development salt when the salt variable is empty")
	seeds := flag.String("seeds", "", "comma separated explicit seeds to run instead of hidden ones")
	src := flag.String("seedfall", "", "seedfall source directory, default SEEDFALL_SRC or the module the grader links")
	jobs := flag.Int("j", 0, "parallel runs, default all CPUs")
	asJSON := flag.Bool("json", true, "print the full report as JSON, otherwise a one line summary")
	flag.Parse()
	if *taskDir == "" || *workspace == "" {
		return fmt.Errorf("-task and -workspace are required")
	}
	task, err := gym.LoadTask(*taskDir)
	if err != nil {
		return err
	}
	source, err := gym.SeedfallSource(*src)
	if err != nil {
		return err
	}
	explicit := *seeds != ""
	var list []uint64
	if explicit {
		if list, err = parseSeeds(*seeds); err != nil {
			return err
		}
	} else {
		salt, err := gym.Salt(*saltEnv, *dev)
		if err != nil {
			return err
		}
		list = gym.HiddenSeeds(salt, task.ID, task.Seeds(*n))
	}
	rep, err := gym.Grade(gym.Options{Task: task, Workspace: *workspace, SeedfallSrc: source, Seeds: list, Explicit: explicit, Jobs: *jobs})
	if err != nil {
		return err
	}
	if !*asJSON {
		fmt.Printf("%s passed %d/%d reward %.3f build_ok %t guard_tripped %t build %.1fs run %.1fs\n", rep.Task, rep.Passed, rep.Total, rep.Reward, rep.BuildOK, rep.Guard.Tripped, rep.BuildSeconds, rep.RunSeconds)
		return nil
	}
	return json.NewEncoder(os.Stdout).Encode(rep)
}

func parseSeeds(s string) ([]uint64, error) {
	var out []uint64
	for _, part := range strings.Split(s, ",") {
		v, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad seed %q", part)
		}
		out = append(out, v)
	}
	return out, nil
}
