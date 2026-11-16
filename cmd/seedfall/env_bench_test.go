package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const benchConfig = "../../examples/etcdraft/opcluster.json"

func benchHost(b *testing.B, jobs int) *envHost {
	b.Helper()
	if testing.Short() {
		b.Skip("builds the opcluster guests")
	}
	h := newEnvHost(jobs)
	b.Cleanup(h.close)
	t, err := h.load(engine.ConfigRef{Path: filepath.FromSlash(benchConfig)})
	if err != nil {
		b.Fatal(err)
	}
	h.target = t
	return h
}

func benchReset(b *testing.B, h *envHost, seed uint64) {
	b.Helper()
	if _, err := h.reset(envRequest{Config: filepath.FromSlash(benchConfig), Seed: seed}); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkEnvReset(b *testing.B) {
	h := benchHost(b, 1)
	b.ResetTimer()
	for i := range b.N {
		benchReset(b, h, uint64(i+1))
		b.StopTimer()
		h.close()
		b.StartTimer()
	}
}

func BenchmarkEnvStep(b *testing.B) {
	steps := map[string]func(h *envHost) error{
		"action":       func(h *envHost) error { return h.session.Do(engine.Op{Kind: engine.OpHeal}) },
		"advance100ms": func(h *envHost) error { return h.session.Advance(100 * time.Millisecond) },
	}
	for name, step := range steps {
		b.Run(name, func(b *testing.B) {
			h := benchHost(b, 1)
			benchReset(b, h, 1)
			b.ResetTimer()
			for range b.N {
				if h.session.Peek().Over {
					b.StopTimer()
					benchReset(b, h, 1)
					b.StartTimer()
				}
				if err := step(h); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkEnvEpisode(b *testing.B) {
	for _, jobs := range []int{1, 8} {
		b.Run(map[int]string{1: "j1", 8: "j8"}[jobs], func(b *testing.B) {
			h := benchHost(b, jobs)
			benchReset(b, h, 1)
			ops, err := readOps("../../examples/etcdraft/ops/good.json", nil)
			if err != nil {
				b.Fatal(err)
			}
			seeds := make([]uint64, jobs)
			var episodes int
			b.ResetTimer()
			for i := range b.N {
				for k := range seeds {
					seeds[k] = uint64(i*jobs + k + 1)
				}
				h.replayAll(seeds, ops)
				episodes += jobs
			}
			b.ReportMetric(float64(episodes)/b.Elapsed().Seconds(), "episodes/s")
		})
	}
}

func BenchmarkEnvMemory(b *testing.B) {
	for _, open := range []int{1, 8} {
		for _, until := range []time.Duration{3 * time.Second, 30 * time.Second} {
			b.Run(fmt.Sprintf("open%d/until%s", open, until), func(b *testing.B) {
				h := benchHost(b, 1)
				b.ResetTimer()
				for range b.N {
					runtime.GC()
					var before runtime.MemStats
					runtime.ReadMemStats(&before)
					sessions := make([]*engine.Session, open)
					for i := range sessions {
						s, err := h.target.run.Operate(uint64(i+1), engine.Schedule{})
						if err != nil {
							b.Fatal(err)
						}
						if err := s.Advance(until); err != nil {
							b.Fatal(err)
						}
						sessions[i] = s
					}
					runtime.GC()
					var after runtime.MemStats
					runtime.ReadMemStats(&after)
					b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/float64(open)/(1<<20), "heap-MiB/episode")
					for _, s := range sessions {
						s.Close()
					}
				}
			})
		}
	}
}
