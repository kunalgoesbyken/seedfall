package engine

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strconv"
)

var propertyFuncs = map[string]PropertyKind{
	"Always":              Always,
	"AlwaysOrUnreachable": AlwaysOrUnreachable,
	"Sometimes":           Sometimes,
	"Reachable":           Reachable,
	"Unreachable":         Unreachable,
}

var propertyPackages = []string{
	"github.com/kunalgoesbyken/seedfall/sim",
	"github.com/antithesishq/antithesis-sdk-go/assert",
}

type CatalogEntry struct {
	Kind     PropertyKind
	Message  string
	Location string
}

type goPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Module     *goModule
}

type goModule struct {
	Path string
	Dir  string
}

func catalog(cfg *Config) ([]CatalogEntry, error) {
	var out []CatalogEntry
	seen := map[string]bool{}
	for _, s := range cfg.all() {
		if s.Pkg == "" || seen[s.Pkg] {
			continue
		}
		seen[s.Pkg] = true
		entries, err := catalogPackage(cfg.Dir, s.Pkg)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
	return out, nil
}

func catalogPackage(dir, pkg string) ([]CatalogEntry, error) {
	cmd := exec.Command("go", "list", "-json", pkg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", pkg, err)
	}
	var p goPackage
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("catalog %s: %w", pkg, err)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var entries []CatalogEntry
	fset := token.NewFileSet()
	for _, name := range p.GoFiles {
		path := filepath.Join(p.Dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("catalog %s: %w", path, err)
		}
		entries = append(entries, fileProperties(fset, root, file)...)
	}
	return entries, nil
}

func fileProperties(fset *token.FileSet, root string, file *ast.File) []CatalogEntry {
	aliases := propertyAliases(file)
	if len(aliases) == 0 {
		return nil
	}
	var out []CatalogEntry
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if entry, ok := propertyCall(call, aliases); ok {
			entry.Location = location(fset, root, call.Pos())
			out = append(out, entry)
		}
		return true
	})
	return out
}

func propertyAliases(file *ast.File) map[string]bool {
	aliases := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !slices.Contains(propertyPackages, path) {
			continue
		}
		name := pathpkg.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		aliases[name] = true
	}
	return aliases
}

func propertyCall(call *ast.CallExpr, aliases map[string]bool) (CatalogEntry, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return CatalogEntry{}, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || !aliases[pkg.Name] {
		return CatalogEntry{}, false
	}
	kind, ok := propertyFuncs[sel.Sel.Name]
	if !ok {
		return CatalogEntry{}, false
	}
	msg, ok := literalArg(call, kind.messageArg())
	if !ok {
		return CatalogEntry{}, false
	}
	return CatalogEntry{Kind: kind, Message: msg}, true
}

func literalArg(call *ast.CallExpr, i int) (string, bool) {
	if i >= len(call.Args) {
		return "", false
	}
	lit, ok := call.Args[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func location(fset *token.FileSet, root string, pos token.Pos) string {
	p := fset.Position(pos)
	if rel, err := filepath.Rel(root, p.Filename); err == nil {
		return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), p.Line)
	}
	return fmt.Sprintf("%s:%d", p.Filename, p.Line)
}
