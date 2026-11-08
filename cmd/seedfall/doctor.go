package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/version"
	"os"
	"strconv"
	"strings"

	"github.com/kunalgoesbyken/seedfall"
	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "fail"
	checkSkip = "skip"

	lowMemoryBytes = 2 << 30
	bytesPerGiB    = 1 << 30
	kibibyte       = 1024
)

type doctorArgs struct{}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status" jsonschema:"ok, warn, fail or skip"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type doctorView struct {
	Version engine.BuildInfo `json:"version"`
	Passed  bool             `json:"passed"`
	Checks  []doctorCheck    `json:"checks"`
}

func cmdDoctor(args []string) error {
	fs := newFlags("doctor")
	jsonOut := fs.Bool("json", false, "print the checks as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	view := diagnose()
	if *jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(view); err != nil {
			return err
		}
	} else {
		printDoctor(view)
	}
	if !view.Passed {
		exit(1)
	}
	return nil
}

func toolDoctor(_ context.Context, _ *mcp.CallToolRequest, _ doctorArgs) (*mcp.CallToolResult, doctorView, error) {
	return nil, diagnose(), nil
}

func printDoctor(view doctorView) {
	fmt.Printf("seedfall %s (%s)\n", view.Version.Version, view.Version.Platform)
	for _, c := range view.Checks {
		fmt.Printf("  %-4s  %-12s  %s\n", c.Status, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Printf("        fix: %s\n", c.Fix)
		}
	}
	if view.Passed {
		fmt.Println("ready")
		return
	}
	fmt.Println("not ready: fix the failed checks above")
}

func diagnose() doctorView {
	goCheck, toolchain := checkGo()
	checks := []doctorCheck{goCheck}
	if toolchain {
		checks = append(checks, checkWasip1(), checkNetOverlay(), checkBuildCache())
	} else {
		for _, name := range []string{"wasip1 build", "net_overlay", "build cache"} {
			checks = append(checks, doctorCheck{Name: name, Status: checkSkip, Detail: "needs a working go command"})
		}
	}
	checks = append(checks, checkWorkDir(), checkMemory())
	view := doctorView{Version: engine.Binary(), Passed: true, Checks: checks}
	for _, c := range checks {
		view.Passed = view.Passed && c.Status != checkFail
	}
	return view
}

func requiredGo() string {
	mod, err := seedfall.Files.ReadFile("go.mod")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(mod)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			return "go" + v
		}
	}
	return ""
}

func checkGo() (doctorCheck, bool) {
	c := doctorCheck{Name: "go"}
	installed, err := engine.GoEnv("GOVERSION")
	if err != nil {
		c.Status, c.Detail = checkFail, "no working go command on PATH: "+err.Error()
		c.Fix = "install Go from https://go.dev/dl/; seedfall builds your nodes with it"
		return c, false
	}
	want := requiredGo()
	if goTooOld(installed, want) {
		c.Status, c.Detail, c.Fix = checkFail, fmt.Sprintf("%s is older than %s", installed, want), "install "+want+" or newer from https://go.dev/dl/"
		return c, true
	}
	c.Status, c.Detail = checkOK, fmt.Sprintf("%s (needs %s or newer)", installed, want)
	return c, true
}

func goTooOld(installed, want string) bool {
	return version.IsValid(installed) && version.Compare(installed, want) < 0
}

func checkWasip1() doctorCheck {
	c := doctorCheck{Name: "wasip1 build", Status: checkOK, Detail: "GOOS=wasip1 GOARCH=wasm builds a program"}
	if err := engine.ProbeWasip1Build(); err != nil {
		c.Status, c.Detail, c.Fix = checkFail, err.Error(), "check that the Go installation is complete and that GOFLAGS does not break a plain go build"
	}
	return c
}

func checkNetOverlay() doctorCheck {
	c := doctorCheck{Name: "net_overlay", Status: checkOK, Detail: "this Go's net package patches and builds for wasip1"}
	if err := engine.ProbeNetOverlay(); err != nil {
		c.Status, c.Detail = checkWarn, err.Error()
		c.Fix = `use sim.Listen and sim.Dial instead of "net_overlay": true, or a Go version seedfall supports`
	}
	return c
}

func checkBuildCache() doctorCheck {
	c := doctorCheck{Name: "build cache"}
	dir, err := engine.GoEnv("GOCACHE")
	if err != nil {
		c.Status, c.Detail, c.Fix = checkFail, err.Error(), "set GOCACHE to a writable directory"
		return c
	}
	if err := writable(dir); err != nil {
		c.Status, c.Detail, c.Fix = checkFail, fmt.Sprintf("%s: %v", dir, err), "set GOCACHE to a writable directory"
		return c
	}
	c.Status, c.Detail = checkOK, dir+" is writable"
	return c
}

func checkWorkDir() doctorCheck {
	c := doctorCheck{Name: "work dir", Status: checkOK, Detail: "this directory is writable for .seedfall"}
	if err := writable("."); err != nil {
		c.Status, c.Detail, c.Fix = checkWarn, err.Error(), "run seedfall from a writable directory that holds your seedfall.json"
	}
	return c
}

func writable(dir string) error {
	probe, err := os.MkdirTemp(dir, ".seedfall-doctor-")
	if err != nil {
		return err
	}
	return os.Remove(probe)
}

func checkMemory() doctorCheck {
	c := doctorCheck{Name: "free memory"}
	free, ok := freeMemory()
	if !ok {
		c.Status, c.Detail = checkSkip, "not measured on this platform"
		return c
	}
	c.Status, c.Detail = checkOK, fmt.Sprintf("%.1f GiB available", float64(free)/bytesPerGiB)
	if free < lowMemoryBytes {
		c.Status, c.Fix = checkWarn, "close other programs, or lower -j so fewer guests run at once"
	}
	return c
}

func parseMemAvailable(meminfo string) (uint64, bool) {
	for line := range strings.Lines(meminfo) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			return kb * kibibyte, err == nil
		}
	}
	return 0, false
}
