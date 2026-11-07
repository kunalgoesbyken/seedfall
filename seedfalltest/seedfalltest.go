package seedfalltest

import (
	"runtime"
	"strings"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const traceTail = 30

func Sweep(t testing.TB, config, seeds string) {
	t.Helper()
	cfg, err := engine.LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Build(cfg); err != nil {
		t.Fatal(err)
	}
	r, err := engine.NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	lo, hi, err := engine.ParseSeeds(seeds)
	if err != nil {
		t.Fatal(err)
	}
	first, passed, _ := engine.Sweep(r, lo, hi, runtime.NumCPU(), func(engine.Result) {})
	if first == nil {
		t.Logf("seedfall: %d seeds passed", passed)
		return
	}
	trace := first.Trace
	if len(trace) > traceTail {
		trace = trace[len(trace)-traceTail:]
	}
	t.Fatalf("seedfall: seed %d failed at t=%s: %s\n%s\nreplay: seedfall replay -c %s -seed %d",
		first.Seed, first.At, first.Reason, strings.Join(trace, "\n"), config, first.Seed)
}
