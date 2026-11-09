package engine

import (
	"fmt"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func Build(cfg *Config) error {
	specs := cfg.specs()
	if err := os.MkdirAll(filepath.Join(cfg.Dir, ".seedfall"), 0o755); err != nil {
		return err
	}
	flags, err := buildFlags(cfg)
	if err != nil {
		return err
	}
	built := map[string]string{}
	build := func(pkg string) (string, error) {
		if out, ok := built[pkg]; ok {
			return out, nil
		}
		out := filepath.Join(".seedfall", strings.NewReplacer("/", "_", "\\", "_", ".", "_").Replace(pkg)+".wasm")
		cmd := exec.Command("go", append(append([]string{"build"}, flags...), "-o", out, pkg)...)
		cmd.Dir = cfg.Dir
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("build %s: %w", pkg, err)
		}
		built[pkg] = out
		return out, nil
	}
	for _, s := range specs {
		if s.Pkg != "" {
			if s.Wasm, err = build(s.Pkg); err != nil {
				return err
			}
		}
		if s.UpgradeTo != "" {
			if s.upgradeWasm, err = build(s.UpgradeTo); err != nil {
				return err
			}
		}
	}
	return nil
}

func RotateSeeds(lo, hi, rotation uint64) (uint64, uint64, error) {
	high, shift := bits.Mul64(hi-lo+1, rotation)
	last, carry := bits.Add64(hi, shift, 0)
	if high != 0 || carry != 0 {
		return 0, 0, fmt.Errorf("rotation %d moves seeds %d-%d past the largest seed", rotation, lo, hi)
	}
	return lo + shift, last, nil
}

func ParseSeeds(s string) (uint64, uint64, error) {
	lo, hi, found := strings.Cut(s, "-")
	a, err := strconv.ParseUint(lo, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad seed range %q", s)
	}
	b := a
	if found {
		if b, err = strconv.ParseUint(hi, 10, 64); err != nil || b < a {
			return 0, 0, fmt.Errorf("bad seed range %q", s)
		}
	}
	if b == math.MaxUint64 {
		return 0, 0, fmt.Errorf("bad seed range %q: seed %d is the largest uint64, so the count of seeds or the next seed would wrap around, end the range at %d or lower", s, uint64(math.MaxUint64), uint64(math.MaxUint64-1))
	}
	return a, b, nil
}
