package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

func Build(cfg *Config) error {
	specs := []*NodeSpec{&cfg.Workload}
	for i := range cfg.Nodes {
		specs = append(specs, &cfg.Nodes[i])
	}
	if err := os.MkdirAll(filepath.Join(cfg.Dir, ".seedfall"), 0o755); err != nil {
		return err
	}
	built := map[string]string{}
	for _, s := range specs {
		if s.Pkg == "" {
			continue
		}
		if out, ok := built[s.Pkg]; ok {
			s.Wasm = out
			continue
		}
		out := filepath.Join(".seedfall", strings.NewReplacer("/", "_", "\\", "_", ".", "_").Replace(s.Pkg)+".wasm")
		cmd := exec.Command("go", "build", "-o", out, s.Pkg)
		cmd.Dir = cfg.Dir
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s: %w", s.Pkg, err)
		}
		built[s.Pkg], s.Wasm = out, out
	}
	return nil
}

func ParseSeeds(s string) (uint64, uint64, error) {
	lo, hi, found := strings.Cut(s, "-")
	a, err := strconv.ParseUint(lo, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad seed range %q", s)
	}
	if !found {
		return a, a, nil
	}
	b, err := strconv.ParseUint(hi, 10, 64)
	if err != nil || b < a {
		return 0, 0, fmt.Errorf("bad seed range %q", s)
	}
	return a, b, nil
}

func Sweep(r *Runner, lo, hi uint64, jobs int, onResult func(Result)) (*Result, int, int) {
	var (
		next       atomic.Uint64
		mu         sync.Mutex
		first      *Result
		passed     int
		unfinished int
		wg         sync.WaitGroup
	)
	next.Store(lo)
	for range max(jobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				seed := next.Add(1) - 1
				mu.Lock()
				stop := seed > hi || (first != nil && seed > first.Seed)
				mu.Unlock()
				if stop {
					return
				}
				res := r.Run(seed, nil)
				mu.Lock()
				switch {
				case res.Failed && (first == nil || res.Seed < first.Seed):
					first = &res
				case !res.Failed:
					passed++
					if !res.Finished {
						unfinished++
					}
				}
				onResult(res)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return first, passed, unfinished
}
