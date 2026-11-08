package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

func TestNormalizeArgsMapsHelpAndVersionFlags(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"--version"}, []string{"version"}},
		{[]string{"-h"}, []string{"help"}},
		{[]string{"--help", "run"}, []string{"help", "run"}},
		{[]string{"run", "-h"}, []string{"run", "-h"}},
		{nil, nil},
	} {
		if got := normalizeArgs(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%v: got %v", c.in, got)
		}
	}
}

func TestCommandHelpListsFlagsWithDefaults(t *testing.T) {
	for _, name := range []string{"run", "trace grep", "version", "doctor", "help"} {
		cmd, _, ok := findCommand(strings.Fields(name))
		if !ok {
			t.Fatalf("%s not found", name)
		}
		text, err := commandHelp(cmd)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(text, "usage: seedfall "+name) {
			t.Errorf("%s: %q", name, text)
		}
	}
	run, _, _ := findCommand([]string{"run"})
	text, err := commandHelp(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-timeout duration", "(default 1-200)", "-json\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("run help lacks %q:\n%s", want, text)
		}
	}
}

func TestProgressLineOverwritesAndClears(t *testing.T) {
	var b bytes.Buffer
	line := &progressLine{w: &b}
	long := engine.SweepProgress{Done: 100, Total: 1000, Failed: 2, Elapsed: 10 * time.Second, Virtual: 40 * time.Second}
	short := engine.SweepProgress{Done: 1, Total: 2, Elapsed: time.Second, Virtual: time.Second}
	line.update(long)
	first := formatProgress(long)
	if b.String() != "\r"+first {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	line.update(short)
	second := formatProgress(short)
	padded := second + strings.Repeat(" ", len(first)-len(second))
	if b.String() != "\r"+padded {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	line.clear()
	if b.String() != fmt.Sprintf("\r%s\r", strings.Repeat(" ", len(second))) {
		t.Fatalf("%q", b.String())
	}
	if !strings.Contains(first, "100/1000 seeds  10.0/s  2 failed  eta 1m30s  4x virtual") {
		t.Fatalf("%q", first)
	}
	if !strings.Contains(formatProgress(engine.SweepProgress{Total: 4}), "eta "+etaUnknown) {
		t.Fatal("a sweep with nothing done yet has no estimate")
	}
}

func TestParseMemAvailable(t *testing.T) {
	got, ok := parseMemAvailable("MemTotal:  16000 kB\nMemAvailable:   2048 kB\nBuffers: 1 kB\n")
	if !ok || got != 2048*kibibyte {
		t.Fatalf("%d %v", got, ok)
	}
	if _, ok := parseMemAvailable("MemTotal: 16000 kB\n"); ok {
		t.Fatal("found MemAvailable in a file without it")
	}
}

func TestRequiredGoIsTheModuleDirective(t *testing.T) {
	if got := requiredGo(); !strings.HasPrefix(got, "go1.") {
		t.Fatalf("%q", got)
	}
}

func TestGoTooOld(t *testing.T) {
	for installed, want := range map[string]bool{"go1.24.9": true, "go1.25.0": false, "go1.27.1": false, "devel go1.28-abc123 Mon Jan 1": false} {
		if got := goTooOld(installed, "go1.25.0"); got != want {
			t.Errorf("%s: %v", installed, got)
		}
	}
}

func TestHelpRejectsAnUnknownCommand(t *testing.T) {
	if err := cmdHelp([]string{"bogus"}); err == nil {
		t.Fatal("help of an unknown command succeeded")
	}
}

func TestRunToolRefusesANegativeTimeout(t *testing.T) {
	if _, _, err := toolRun(context.Background(), nil, runArgs{Seeds: "1", Timeout: "-1s"}); !errors.Is(err, errNegativeTimeout) {
		t.Fatalf("%v", err)
	}
}

func TestDoctorChecksThisMachine(t *testing.T) {
	view := diagnose()
	if !view.Passed {
		t.Fatalf("doctor failed on the machine building it: %+v", view.Checks)
	}
	names := []string{}
	for _, c := range view.Checks {
		names = append(names, c.Name)
		if c.Status == checkFail || c.Detail == "" {
			t.Errorf("%+v", c)
		}
	}
	if !slices.Equal(names, []string{"go", "wasip1 build", "net_overlay", "build cache", "work dir", "free memory"}) {
		t.Fatalf("%v", names)
	}
}
