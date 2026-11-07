package engine

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed compat
var compatShims embed.FS

const shimSuffix = ".txt"

type shimSet struct {
	files    map[string]string
	provided map[string]bool
}

func writeCompatModule(cfg *Config) (string, error) {
	pkgs, err := listDeps(cfg)
	if err != nil {
		return "", err
	}
	out, err := filepath.Abs(filepath.Join(cfg.Dir, ".seedfall", "compat"))
	if err != nil {
		return "", err
	}
	copies := map[string]string{}
	for _, p := range pkgs {
		shims, err := loadShims(p.ImportPath)
		if err != nil {
			return "", err
		}
		if shims == nil {
			continue
		}
		if p.Module == nil {
			return "", fmt.Errorf("compat overlay: %s is not part of a module", p.ImportPath)
		}
		root, ok := copies[p.Module.Path]
		if !ok {
			root = filepath.Join(out, filepath.FromSlash(p.Module.Path))
			if err := copyModule(p.Module.Dir, root); err != nil {
				return "", fmt.Errorf("compat overlay: %w", err)
			}
			copies[p.Module.Path] = root
		}
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return "", err
		}
		if err := applyShims(p, shims, filepath.Join(root, rel)); err != nil {
			return "", err
		}
	}
	if len(copies) == 0 {
		return "", nil
	}
	return writeCompatModfile(cfg, out, copies)
}

func loadShims(importPath string) (*shimSet, error) {
	dir := path.Join("compat", importPath)
	entries, err := fs.ReadDir(compatShims, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	set := &shimSet{files: map[string]string{}, provided: map[string]bool{}}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := compatShims.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		names, err := topLevelNames(e.Name(), b)
		if err != nil {
			return nil, fmt.Errorf("compat overlay: %s: %w", dir, err)
		}
		for _, n := range names {
			set.provided[n] = true
		}
		set.files[strings.TrimSuffix(e.Name(), shimSuffix)] = string(b)
	}
	if len(set.files) == 0 {
		return nil, nil
	}
	return set, nil
}

func applyShims(p goPackage, shims *shimSet, dir string) error {
	for _, f := range p.GoFiles {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return fmt.Errorf("compat overlay: %w", err)
		}
		names, err := topLevelNames(f, b)
		if err != nil {
			return fmt.Errorf("compat overlay: %s: %w", p.ImportPath, err)
		}
		if !overlaps(names, shims.provided) {
			continue
		}
		if missing := unprovided(names, shims.provided); len(missing) > 0 {
			return fmt.Errorf("compat overlay: %s/%s declares %s, which the wasip1 shim does not provide; this version is not supported yet", p.ImportPath, f, strings.Join(missing, ", "))
		}
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			return err
		}
	}
	for name, content := range shims.files {
		if _, err := writeFile(filepath.Join(dir, name), content); err != nil {
			return err
		}
	}
	return nil
}

func copyModule(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			return filepath.SkipDir
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func writeCompatModfile(cfg *Config, out string, copies map[string]string) (string, error) {
	gomod, err := goEnv(cfg.Dir, "GOMOD")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(gomod) {
		return "", fmt.Errorf("compat overlay: %s is not inside a Go module", cfg.Dir)
	}
	modfile := filepath.Join(out, "go.mod")
	if err := copyFile(gomod, modfile); err != nil {
		return "", err
	}
	if err := copyFile(strings.TrimSuffix(gomod, ".mod")+".sum", filepath.Join(out, "go.sum")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	args := []string{"mod", "edit"}
	for mod, dir := range copies {
		args = append(args, "-replace="+mod+"="+dir)
	}
	cmd := exec.Command("go", append(args, modfile)...)
	cmd.Dir = cfg.Dir
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("compat overlay: go mod edit: %w: %s", err, b)
	}
	return modfile, nil
}

func listDeps(cfg *Config) ([]goPackage, error) {
	args := []string{"list", "-e", "-deps", "-json=ImportPath,Dir,GoFiles,Module"}
	for _, s := range cfg.all() {
		if s.Pkg != "" {
			args = append(args, s.Pkg)
		}
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = cfg.Dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var pkgs []goPackage
	dec := json.NewDecoder(stdout)
	for {
		var p goPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	return pkgs, nil
}

func topLevelNames(name string, src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			names = append(names, receiverPrefix(d.Recv)+d.Name.Name)
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name != "_" {
							names = append(names, n.Name)
						}
					}
				}
			}
		}
	}
	return names, nil
}

func receiverPrefix(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.ParenExpr:
			t = x.X
		case *ast.Ident:
			return x.Name + "."
		default:
			return ""
		}
	}
}

func overlaps(names []string, provided map[string]bool) bool {
	for _, n := range names {
		if provided[n] {
			return true
		}
	}
	return false
}

func unprovided(names []string, provided map[string]bool) []string {
	var missing []string
	for _, n := range names {
		if !provided[n] {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	return missing
}
