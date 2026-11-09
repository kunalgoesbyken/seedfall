package main

import (
	"flag"
	"strings"
	"testing"
)

func TestFlagValidators(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Int("batch", 20, "")
	fs.Int("other", 0, "")
	if err := fs.Parse([]string{"-other", "1"}); err != nil {
		t.Fatal(err)
	}
	if flagSet(fs, "batch") || !flagSet(fs, "other") {
		t.Error("flagSet does not tell a given flag from a default")
	}
	for _, c := range []struct {
		name string
		err  error
		bad  bool
	}{
		{"jobs 1", checkJobs(1), false},
		{"jobs 0", checkJobs(0), true},
		{"jobs -2", checkJobs(-2), true},
		{"nonnegative", nonNegative(namedInt{"-a", 0}, namedInt{"-b", 3}), false},
		{"negative", nonNegative(namedInt{"-a", 0}, namedInt{"-b", -3}), true},
		{"batch default without novelty", checkBatch(20, false, 0), false},
		{"batch with novelty", checkBatch(5, true, 3), false},
		{"batch without novelty", checkBatch(5, true, 0), true},
		{"negative batch", checkBatch(-1, false, 3), true},
		{"mutants with app", checkMutants(5, true, true), false},
		{"default mutants without app", checkMutants(10, false, false), false},
		{"mutants without app", checkMutants(5, true, false), true},
		{"negative mutants", checkMutants(-1, true, true), true},
		{"repo", checkRepo("kunal-x/seed.fall_1"), false},
		{"repo without owner", checkRepo("seedfall"), true},
	} {
		if (c.err != nil) != c.bad {
			t.Errorf("%s: %v", c.name, c.err)
		}
	}
}

func TestCommandsRejectBadCountsBeforeLoadingTheConfig(t *testing.T) {
	for _, c := range []struct {
		run  func([]string) error
		args []string
		want string
	}{
		{cmdRun, []string{"-j", "0"}, "-j"},
		{cmdRun, []string{"-batch", "5"}, "-batch"},
		{cmdRun, []string{"-batch", "-1", "-until-novelty", "2"}, "-batch"},
		{cmdExplore, []string{"-j", "-1"}, "-j"},
		{cmdExplore, []string{"-rounds", "-1"}, "-rounds"},
		{cmdExplore, []string{"-batch", "-1"}, "-batch"},
		{cmdCause, []string{"-seed", "1", "-j", "0"}, "-j"},
		{cmdCause, []string{"-seed", "1", "-points", "-1"}, "-points"},
		{cmdCause, []string{"-seed", "1", "-branches", "-1"}, "-branches"},
		{cmdShrink, []string{"-seed", "1", "-j", "0"}, "-j"},
		{cmdMutate, []string{"-mutants", "5"}, "-mutants"},
		{cmdMutate, []string{"-j", "0"}, "-j"},
		{cmdVerify, []string{"-seed", "1", "-j", "0"}, "-j"},
	} {
		err := c.run(append([]string{"-c", "missing.json"}, c.args...))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
}
