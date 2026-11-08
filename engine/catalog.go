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

type propertyFunc struct {
	kind       PropertyKind
	messageArg int
}

const (
	reachMessageArg   = 0
	condMessageArg    = 1
	groupedMessageArg = 1
	numericMessageArg = 2
)

var propertyFuncs = map[string]propertyFunc{
	"Always":                        {Always, condMessageArg},
	"AlwaysOrUnreachable":           {AlwaysOrUnreachable, condMessageArg},
	"Sometimes":                     {Sometimes, condMessageArg},
	"Reachable":                     {Reachable, reachMessageArg},
	"Unreachable":                   {Unreachable, reachMessageArg},
	"Eventually":                    {Eventually, condMessageArg},
	"AlwaysGreaterThan":             {Always, numericMessageArg},
	"AlwaysGreaterThanOrEqualTo":    {Always, numericMessageArg},
	"AlwaysLessThan":                {Always, numericMessageArg},
	"AlwaysLessThanOrEqualTo":       {Always, numericMessageArg},
	"SometimesGreaterThan":          {Sometimes, numericMessageArg},
	"SometimesGreaterThanOrEqualTo": {Sometimes, numericMessageArg},
	"SometimesLessThan":             {Sometimes, numericMessageArg},
	"SometimesLessThanOrEqualTo":    {Sometimes, numericMessageArg},
	"AlwaysSome":                    {Always, groupedMessageArg},
	"SometimesAll":                  {Sometimes, groupedMessageArg},
}

const simPackage = "github.com/kunalgoesbyken/seedfall/sim"

var propertyPackages = []string{
	simPackage,
	"github.com/antithesishq/antithesis-sdk-go/assert",
}

var buggifyFuncs = map[string]bool{"Buggify": true, "BuggifyWithProb": true}

type CatalogEntry struct {
	Kind     PropertyKind
	Message  string
	Location string
	Buggify  bool
}

type goPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Ignored    []string `json:"IgnoredGoFiles"`
	Module     *goModule
}

type goModule struct {
	Path string
	Dir  string
	Main bool
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

func listPackage(dir, pkg string) (goPackage, error) {
	cmd := exec.Command("go", "list", "-json", pkg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := cmd.Output()
	if err != nil {
		return goPackage{}, fmt.Errorf("go list %s: %w", pkg, err)
	}
	var p goPackage
	if err := json.Unmarshal(out, &p); err != nil {
		return goPackage{}, fmt.Errorf("go list %s: %w", pkg, err)
	}
	return p, nil
}

func catalogPackage(dir, pkg string) ([]CatalogEntry, error) {
	p, err := listPackage(dir, pkg)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
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

func propertyAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !slices.Contains(propertyPackages, path) {
			continue
		}
		name := pathpkg.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		aliases[name] = path
	}
	return aliases
}

func propertyCall(call *ast.CallExpr, aliases map[string]string) (CatalogEntry, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return CatalogEntry{}, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || aliases[pkg.Name] == "" {
		return CatalogEntry{}, false
	}
	if aliases[pkg.Name] == simPackage && buggifyFuncs[sel.Sel.Name] {
		site, ok := literalArg(call, 0)
		return CatalogEntry{Message: site, Buggify: true}, ok
	}
	fn, ok := propertyFuncs[sel.Sel.Name]
	if !ok {
		return CatalogEntry{}, false
	}
	msg, ok := literalArg(call, fn.messageArg)
	if !ok {
		return CatalogEntry{}, false
	}
	return CatalogEntry{Kind: fn.kind, Message: msg}, true
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
