package engine

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const simImportPath = "github.com/kunalgoesbyken/seedfall/sim"

var catalogKinds = map[string]PropertyKind{
	"Always":      Always,
	"Sometimes":   Sometimes,
	"Reachable":   Reachable,
	"Unreachable": Unreachable,
}

type CatalogEntry struct {
	Kind     PropertyKind
	Message  string
	Location string
}

type goPackage struct {
	Dir     string
	GoFiles []string
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
	alias, ok := simAlias(file)
	if !ok {
		return nil
	}
	var out []CatalogEntry
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if entry, ok := propertyCall(call, alias); ok {
			entry.Location = location(fset, root, call.Pos())
			out = append(out, entry)
		}
		return true
	})
	return out
}

func simAlias(file *ast.File) (string, bool) {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != simImportPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return "sim", true
	}
	return "", false
}

func propertyCall(call *ast.CallExpr, alias string) (CatalogEntry, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return CatalogEntry{}, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != alias {
		return CatalogEntry{}, false
	}
	kind, ok := catalogKinds[sel.Sel.Name]
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
