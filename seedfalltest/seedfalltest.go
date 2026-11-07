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
	sum := engine.Sweep(r, lo, hi, engine.SweepOptions{Jobs: runtime.NumCPU()})
	first := sum.First
	if first == nil {
		if unmet := sum.Coverage.Unmet(); len(unmet) > 0 {
			t.Fatalf("seedfall: properties never reached or never true in %d seeds: %s", sum.Passed, strings.Join(unmet, "; "))
		}
		t.Logf("seedfall: %d seeds passed", sum.Passed)
		return
	}
	trace := first.Trace
	if len(trace) > traceTail {
		trace = trace[len(trace)-traceTail:]
	}
	t.Fatalf("seedfall: seed %d failed at t=%s: %s\n%s\nreplay: seedfall replay -c %s -seed %d",
		first.Seed, first.At, first.Reason, strings.Join(trace, "\n"), config, first.Seed)
}
