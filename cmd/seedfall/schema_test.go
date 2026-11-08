package main

import (
	"slices"
	"testing"
)

func TestSchemaDescribesEveryCommandAndTool(t *testing.T) {
	s, err := surface()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commands) != len(commands) {
		t.Fatalf("%d commands described, %d registered", len(s.Commands), len(commands))
	}
	flags := map[string][]string{}
	for _, c := range s.Commands {
		for _, f := range c.Flags {
			flags[c.Command] = append(flags[c.Command], f.Name)
		}
	}
	for command, want := range map[string][]string{
		"seedfall run":        {"c", "seeds", "report", "json"},
		"seedfall faults":     {"c", "seed", "faults", "remove", "shift", "add", "out"},
		"seedfall trace grep": {"c", "seed", "e", "context", "node", "from", "to", "limit", "json"},
		"seedfall findings":   {"report", "history", "status", "json"},
		"seedfall pin":        {"c", "seed", "faults"},
	} {
		for _, name := range want {
			if !slices.Contains(flags[command], name) {
				t.Errorf("%s has no -%s in %v", command, name, flags[command])
			}
		}
	}
	var tools []string
	for _, tl := range s.Tools {
		tools = append(tools, tl.Name)
		if tl.InputSchema == nil || tl.Description == "" {
			t.Errorf("%s has no input schema or description", tl.Name)
		}
	}
	for _, name := range []string{"seedfall_mutate", "seedfall_findings", "seedfall_trace_search", "seedfall_faults_edit", "seedfall_pin", "seedfall_logs"} {
		if !slices.Contains(tools, name) {
			t.Errorf("%s is not in %v", name, tools)
		}
	}
	if len(s.Prompts) == 0 || len(s.Resources) == 0 {
		t.Errorf("%d prompts, %d resources", len(s.Prompts), len(s.Resources))
	}
}

func TestFindCommandTakesSubcommands(t *testing.T) {
	for _, c := range []struct {
		args []string
		name string
		rest int
	}{
		{[]string{"trace", "grep", "-e", "x"}, "trace grep", 2},
		{[]string{"run", "-seeds", "1-5"}, "run", 2},
		{[]string{"findings"}, "findings", 0},
	} {
		cmd, rest, ok := findCommand(c.args)
		if !ok || cmd.name != c.name || len(rest) != c.rest {
			t.Errorf("%v: %q %v %t", c.args, cmd.name, rest, ok)
		}
	}
	for _, args := range [][]string{{"trace"}, {"report", "post"}, {}} {
		if _, _, ok := findCommand(args); ok {
			t.Errorf("%v found a command", args)
		}
	}
}
