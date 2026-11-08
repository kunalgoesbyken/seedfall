package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	FlipComparison  = "flip_comparison"
	SkipErrorReturn = "skip_error_return"
	SkipSync        = "skip_sync"

	MutantUnbuilt MutantOutcome = "did not build"
)

var flippedOps = map[token.Token]token.Token{
	token.LSS: token.GEQ, token.GEQ: token.LSS,
	token.GTR: token.LEQ, token.LEQ: token.GTR,
	token.EQL: token.NEQ, token.NEQ: token.EQL,
}

type AppMutant struct {
	Pkg      string `json:"pkg"`
	Location string `json:"location"`
	Op       string `json:"op"`
	From     string `json:"from"`
	To       string `json:"to"`
	file     string
	start    int
	end      int
	with     string
}

func (m AppMutant) Describe() string {
	return fmt.Sprintf("%s %s: %s -> %s", m.Location, m.Op, m.From, m.To)
}

type AppMutantResult struct {
	Mutant     AppMutant
	KilledBy   *Result
	Passed     int
	BuildError string
}

func (m AppMutantResult) Outcome() MutantOutcome {
	switch {
	case m.BuildError != "":
		return MutantUnbuilt
	case m.KilledBy != nil:
		return MutantKilled
	}
	return MutantSurvived
}

func AppMutants(r *Runner) ([]AppMutant, error) {
	var out []AppMutant
	seen := map[string]bool{}
	for _, s := range r.cfg.Nodes {
		if s.Pkg == "" || seen[s.Pkg] {
			continue
		}
		seen[s.Pkg] = true
		found, err := packageMutants(r.cfg.Dir, s.Pkg)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

func packageMutants(dir, pkg string) ([]AppMutant, error) {
	p, err := listPackage(dir, pkg)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var out []AppMutant
	for _, name := range p.GoFiles {
		path := filepath.Join(p.Dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("mutants %s: %w", path, err)
		}
		for _, m := range fileMutants(fset, file, src) {
			m.Pkg, m.file, m.Location = pkg, path, location(fset, root, fset.File(file.Pos()).Pos(m.start))
			out = append(out, m)
		}
	}
	return out, nil
}

func fileMutants(fset *token.FileSet, file *ast.File, src []byte) []AppMutant {
	offset := func(p token.Pos) int { return fset.Position(p).Offset }
	text := func(n ast.Node) string { return string(src[offset(n.Pos()):offset(n.End())]) }
	var out []AppMutant
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if to, ok := flippedOps[n.Op]; ok {
				start := offset(n.OpPos)
				out = append(out, AppMutant{Op: FlipComparison, From: text(n), To: text(n.X) + " " + to.String() + " " + text(n.Y), start: start, end: start + len(n.Op.String()), with: to.String()})
			}
		case *ast.IfStmt:
			if earlyReturn(n) {
				to := "false && " + text(n.Cond)
				out = append(out, AppMutant{Op: SkipErrorReturn, From: text(n.Cond), To: to, start: offset(n.Cond.Pos()), end: offset(n.Cond.End()), with: to})
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sync" && len(n.Args) == 0 {
				to := "func() error { _ = " + text(sel.X) + "; return nil }()"
				out = append(out, AppMutant{Op: SkipSync, From: text(n), To: to, start: offset(n.Pos()), end: offset(n.End()), with: to})
			}
		}
		return true
	})
	return out
}

func earlyReturn(n *ast.IfStmt) bool {
	cond, ok := n.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ || len(n.Body.List) == 0 {
		return false
	}
	_, isIdent := cond.X.(*ast.Ident)
	nilIdent, isNil := cond.Y.(*ast.Ident)
	_, returns := n.Body.List[len(n.Body.List)-1].(*ast.ReturnStmt)
	return isIdent && isNil && nilIdent.Name == "nil" && returns
}

func SelectAppMutants(all []AppMutant, n int, seed uint64) []AppMutant {
	if n <= 0 || n >= len(all) {
		return all
	}
	picked := slices.Clone(all)
	r := streams(seed, "app-mutants")
	r.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
	picked = picked[:n]
	slices.SortStableFunc(picked, func(a, b AppMutant) int { return cmp.Or(cmp.Compare(a.file, b.file), cmp.Compare(a.start, b.start)) })
	return picked
}

func SweepAppMutants(r *Runner, mutants []AppMutant, lo, hi uint64, opts SweepOptions) []AppMutantResult {
	out := make([]AppMutantResult, 0, len(mutants))
	for _, m := range mutants {
		out = append(out, sweepAppMutant(r, m, lo, hi, opts))
	}
	return out
}

func sweepAppMutant(r *Runner, m AppMutant, lo, hi uint64, opts SweepOptions) AppMutantResult {
	dir, err := mutantDir(r.cfg)
	if err != nil {
		return AppMutantResult{Mutant: m, BuildError: err.Error()}
	}
	defer os.RemoveAll(dir)
	cfg, err := buildAppMutant(r.cfg, dir, m)
	if err != nil {
		return AppMutantResult{Mutant: m, BuildError: err.Error()}
	}
	mr, err := NewRunner(cfg)
	if err != nil {
		return AppMutantResult{Mutant: m, BuildError: err.Error()}
	}
	defer mr.Close()
	sum := Sweep(mr, lo, hi, opts)
	return AppMutantResult{Mutant: m, KilledBy: sum.First, Passed: sum.Passed}
}

func mutantDir(cfg *Config) (string, error) {
	root, err := filepath.Abs(filepath.Join(cfg.Dir, ".seedfall"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "mutant-")
}

func buildAppMutant(base *Config, dir string, m AppMutant) (*Config, error) {
	src, err := os.ReadFile(m.file)
	if err != nil {
		return nil, err
	}
	mutated, err := writeFile(filepath.Join(dir, filepath.Base(m.file)), string(src[:m.start])+m.with+string(src[m.end:]))
	if err != nil {
		return nil, err
	}
	flags, err := buildFlags(base)
	if err != nil {
		return nil, err
	}
	flags, err = withOverlay(flags, dir, map[string]string{m.file: mutated})
	if err != nil {
		return nil, err
	}
	out := filepath.Join(dir, "mutant.wasm")
	cmd := exec.Command("go", append(append([]string{"build"}, flags...), "-o", out, m.Pkg)...)
	cmd.Dir = base.Dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(msg)))
	}
	cfg := *base
	cfg.Nodes = slices.Clone(base.Nodes)
	for j := range cfg.Nodes {
		if cfg.Nodes[j].Pkg == m.Pkg {
			cfg.Nodes[j].Wasm = out
		}
	}
	return &cfg, nil
}

func withOverlay(flags []string, dir string, replace map[string]string) ([]string, error) {
	i := slices.Index(flags, "-overlay")
	if i >= 0 {
		b, err := os.ReadFile(flags[i+1])
		if err != nil {
			return nil, err
		}
		var existing struct{ Replace map[string]string }
		if err := json.Unmarshal(b, &existing); err != nil {
			return nil, err
		}
		for k, v := range existing.Replace {
			replace[k] = v
		}
		flags = slices.Delete(slices.Clone(flags), i, i+2)
	}
	b, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return nil, err
	}
	overlay, err := writeFile(filepath.Join(dir, "overlay.json"), string(b))
	return append(flags, "-overlay", overlay), err
}

func (r *Runner) Close() { r.rt.Close(context.Background()) }
