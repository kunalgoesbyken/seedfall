package engine

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
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
	needsUnix := false
	for _, p := range pkgs {
		shims, err := loadShims(p.ImportPath)
		if err != nil {
			return "", err
		}
		widened, err := widen(p, shims)
		if err != nil {
			return "", err
		}
		if shims == nil && len(widened) == 0 {
			continue
		}
		if p.Module == nil {
			return "", fmt.Errorf("compat overlay: %s is not part of a module", p.ImportPath)
		}
		root, err := moduleCopy(copies, out, *p.Module)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return "", err
		}
		dir := filepath.Join(root, rel)
		if shims != nil {
			if err := applyShims(p, shims, dir); err != nil {
				return "", err
			}
			continue
		}
		for _, f := range widened {
			if err := f.write(dir); err != nil {
				return "", err
			}
			needsUnix = needsUnix || f.unix
		}
	}
	if needsUnix {
		if err := addUnixShim(cfg, copies, out); err != nil {
			return "", err
		}
	}
	if len(copies) == 0 {
		return "", nil
	}
	return writeCompatModfile(cfg, out, copies)
}

type CompatDep struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Ignored    []string
	Module     string
	MainModule bool
}

func CompatCoverage(d CompatDep) (string, error) {
	shims, err := loadShims(d.ImportPath)
	if err != nil {
		return "", err
	}
	if shims != nil {
		return "shim for " + d.ImportPath, nil
	}
	p := goPackage{ImportPath: d.ImportPath, Dir: d.Dir, GoFiles: d.GoFiles, Ignored: d.Ignored, Module: &goModule{Path: d.Module, Main: d.MainModule}}
	widened, err := widen(p, nil)
	if err != nil || len(widened) == 0 {
		return "", err
	}
	return "generic mmap and flock shim", nil
}

func moduleCopy(copies map[string]string, out string, mod goModule) (string, error) {
	root, ok := copies[mod.Path]
	if ok {
		return root, nil
	}
	root = filepath.Join(out, filepath.FromSlash(mod.Path))
	if err := copyModule(mod.Dir, root); err != nil {
		return "", fmt.Errorf("compat overlay: %w", err)
	}
	copies[mod.Path] = root
	return root, nil
}

const unixModule, unixPackage = "golang.org/x/sys", "golang.org/x/sys/unix"

func addUnixShim(cfg *Config, copies map[string]string, out string) error {
	cmd := exec.Command("go", "list", "-m", "-json", unixModule)
	cmd.Dir = cfg.Dir
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("compat overlay: finding %s: %w", unixModule, err)
	}
	var mod goModule
	if err := json.Unmarshal(b, &mod); err != nil {
		return fmt.Errorf("compat overlay: %s: %w", unixModule, err)
	}
	if mod.Dir == "" {
		return fmt.Errorf("compat overlay: %s is not downloaded; run go mod download %s", unixModule, unixModule)
	}
	root, err := moduleCopy(copies, out, mod)
	if err != nil {
		return err
	}
	shims, err := loadShims(unixPackage)
	if err != nil {
		return err
	}
	return applyShims(goPackage{ImportPath: unixPackage}, shims, filepath.Join(root, "unix"))
}

type widenedFile struct {
	name    string
	source  []byte
	removes []string
	unix    bool
}

func (f widenedFile) write(dir string) error {
	for _, name := range f.removes {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, f.name), f.source, 0o644)
}

func widen(p goPackage, shims *shimSet) ([]widenedFile, error) {
	if shims != nil || p.Module == nil || p.Module.Main {
		return nil, nil
	}
	var out []widenedFile
	active := p.GoFiles
	for _, name := range p.Ignored {
		if strings.HasSuffix(name, "_test.go") || !matchesLinuxOnly(p.Dir, name) {
			continue
		}
		f, ok, err := widenFile(p, name, active)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, f)
			active = slices.DeleteFunc(slices.Clone(active), func(a string) bool { return slices.Contains(f.removes, a) })
		}
	}
	return out, nil
}

func matchesLinuxOnly(dir, name string) bool {
	if strings.HasSuffix(strings.TrimSuffix(name, ".go"), "_linux") {
		return false
	}
	linux := build.Default
	linux.GOOS, linux.GOARCH, linux.CgoEnabled = "linux", "wasm", false
	matched, err := linux.MatchFile(dir, name)
	return err == nil && matched
}

func widenFile(p goPackage, name string, active []string) (widenedFile, bool, error) {
	src, err := os.ReadFile(filepath.Join(p.Dir, name))
	if err != nil {
		return widenedFile{}, false, err
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil || !callsMapOrLock(parsed) {
		return widenedFile{}, false, nil
	}
	names, err := topLevelNames(name, src)
	if err != nil {
		return widenedFile{}, false, fmt.Errorf("compat overlay: %s: %w", p.ImportPath, err)
	}
	provided := map[string]bool{}
	for _, n := range names {
		provided[n] = true
	}
	var removes []string
	for _, other := range active {
		b, err := os.ReadFile(filepath.Join(p.Dir, other))
		if err != nil {
			return widenedFile{}, false, err
		}
		otherNames, err := topLevelNames(other, b)
		if err != nil {
			return widenedFile{}, false, fmt.Errorf("compat overlay: %s: %w", p.ImportPath, err)
		}
		if overlaps(otherNames, provided) {
			if len(unprovided(otherNames, provided)) > 0 {
				return widenedFile{}, false, nil
			}
			removes = append(removes, other)
		}
	}
	return widenedFile{name: name, source: wasip1Source(src, fset.Position(parsed.Package).Offset), removes: removes, unix: importsUnix(parsed)}, true, nil
}

func callsMapOrLock(f *ast.File) bool {
	found := false
	pkgs := shimmedImportNames(f)
	ast.Inspect(f, func(n ast.Node) bool {
		s, ok := n.(*ast.SelectorExpr)
		if !ok || found {
			return !found
		}
		pkg, ok := s.X.(*ast.Ident)
		found = ok && slices.Contains(pkgs, pkg.Name) && slices.Contains(widenedCalls, s.Sel.Name)
		return !found
	})
	return found
}

var widenedCalls = []string{"Mmap", "Munmap", "Msync", "Flock"}

func shimmedImportNames(f *ast.File) []string {
	var names []string
	for _, s := range f.Imports {
		p, err := strconv.Unquote(s.Path.Value)
		if err != nil || (p != "syscall" && p != unixPackage) {
			continue
		}
		name := path.Base(p)
		if s.Name != nil {
			name = s.Name.Name
		}
		names = append(names, name)
	}
	return names
}

func importsUnix(f *ast.File) bool {
	return slices.ContainsFunc(f.Imports, func(s *ast.ImportSpec) bool { return s.Path.Value == fmt.Sprintf("%q", unixPackage) })
}

func wasip1Source(src []byte, pkg int) []byte {
	var header strings.Builder
	for _, line := range strings.SplitAfter(string(src[:pkg]), "\n") {
		if trimmed := strings.TrimSpace(line); !constraint.IsGoBuild(trimmed) && !constraint.IsPlusBuild(trimmed) {
			header.WriteString(line)
		}
	}
	return []byte("//go:build wasip1\n\n" + header.String() + string(src[pkg:]))
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
	args := []string{"list", "-e", "-deps", "-json=ImportPath,Dir,GoFiles,IgnoredGoFiles,Module"}
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
