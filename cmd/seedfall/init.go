package main

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	seedfallModule  = "github.com/kunalgoesbyken/seedfall"
	simPackage      = seedfallModule + "/sim"
	defaultWorkload = "seedfall/workload"
	workloadName    = "workload"
	guessedPort     = "8080"
	errorLines      = 6
)

//go:embed workload.go.txt
var workloadSource []byte

var (
	wasip1Env = []string{"GOOS=wasip1", "GOARCH=wasm"}
	cgoEnv    = []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"}
)

type initArgs struct {
	Dir      string   `json:"dir,omitempty" jsonschema:"Go module directory to onboard; default is the server's working directory"`
	Config   string   `json:"config,omitempty" jsonschema:"config file to write, relative to dir; default seedfall.json"`
	Nodes    []string `json:"nodes,omitempty" jsonschema:"main packages to run as crashable nodes, for example ./cmd/server; default is every main package that imports net"`
	Workload string   `json:"workload,omitempty" jsonschema:"directory for the workload skeleton, relative to dir; default seedfall/workload"`
	DryRun   bool     `json:"dry_run,omitempty" jsonschema:"only inspect and report, write nothing"`
}

type listedPackage struct {
	ImportPath string
	Name       string
	Dir        string
	Standard   bool
	DepOnly    bool
	CgoFiles   []string
	Imports    []string
	Deps       []string
	Module     *struct{ Path string }
	Error      *struct{ Err string }
}

type listedModule struct {
	Path  string
	Error *struct{ Err string }
}

type mainPackage struct {
	Pkg        string `json:"pkg"`
	ImportPath string `json:"import_path"`
	Name       string `json:"node_name,omitempty"`
	ImportsNet bool   `json:"imports_net"`
	UsesSim    bool   `json:"uses_sim"`
	Node       bool   `json:"node"`
}

type blocker struct {
	Package   string   `json:"package"`
	Module    string   `json:"module,omitempty"`
	OwnModule bool     `json:"own_module"`
	Chain     []string `json:"import_chain"`
	Reason    string   `json:"reason,omitempty"`
}

type initView struct {
	Module           string        `json:"module"`
	Dir              string        `json:"dir"`
	RequiresSeedfall bool          `json:"requires_seedfall"`
	Mains            []mainPackage `json:"main_packages"`
	Blockers         []blocker     `json:"wasip1_blockers"`
	Cgo              []blocker     `json:"cgo_packages"`
	ConfigPath       string        `json:"config_path"`
	Config           engine.Config `json:"config"`
	Wrote            []string      `json:"wrote"`
	Kept             []string      `json:"kept_existing"`
	Next             []string      `json:"next_steps"`
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	var a initArgs
	fs.StringVar(&a.Dir, "dir", ".", "Go module directory to onboard")
	fs.StringVar(&a.Config, "c", "seedfall.json", "config file to write, relative to -dir")
	fs.StringVar(&a.Workload, "workload", defaultWorkload, "directory for the workload skeleton, relative to -dir")
	fs.BoolVar(&a.DryRun, "n", false, "only inspect and report, write nothing")
	jsonOut := fs.Bool("json", false, "print the report as JSON")
	fs.Func("node", "main package to run as a crashable node (repeatable); default is every main package that imports net", func(s string) error {
		a.Nodes = append(a.Nodes, s)
		return nil
	})
	fs.Parse(args)
	v, err := runInit(a)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	printInit(v)
	return nil
}

func toolInit(_ context.Context, _ *mcp.CallToolRequest, a initArgs) (*mcp.CallToolResult, initView, error) {
	v, err := runInit(a)
	return nil, v, err
}

func runInit(a initArgs) (initView, error) {
	dir, err := filepath.Abs(cmp.Or(a.Dir, "."))
	if err != nil {
		return initView{}, err
	}
	a.Config, a.Workload = cmp.Or(a.Config, "seedfall.json"), filepath.ToSlash(cmp.Or(a.Workload, defaultWorkload))
	module, err := mainModule(dir)
	if err != nil {
		return initView{}, err
	}
	listed, err := goList(dir, wasip1Env, "-export", "-deps", "./...")
	if err != nil {
		return initView{}, err
	}
	v := initView{Module: module, Dir: dir, RequiresSeedfall: requiresSeedfall(dir, module), ConfigPath: filepath.Join(dir, a.Config)}
	v.Mains, err = mainPackages(dir, listed, a)
	if err != nil {
		return initView{}, err
	}
	nodes := selected(v.Mains)
	v.Blockers = wasip1Blockers(listed, nodes, module)
	if len(nodes) > 0 {
		hostListed, err := goList(dir, cgoEnv, append([]string{"-deps"}, importPaths(nodes)...)...)
		if err != nil {
			return initView{}, err
		}
		v.Cgo = cgoPackages(hostListed, nodes, module)
	}
	v.Config = starterConfig(nodes, "./"+a.Workload)
	v.Next = nextSteps(v, a, nodes)
	if a.DryRun {
		return v, nil
	}
	return v, scaffold(&v, a)
}

func goList(dir string, env []string, args ...string) ([]listedPackage, error) {
	cmd := exec.Command("go", append([]string{"list", "-e", "-json"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			return pkgs, nil
		}
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
}

func goListModule(dir string, args ...string) (listedModule, error) {
	var m listedModule
	cmd := exec.Command("go", append([]string{"list", "-m", "-e", "-json"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return m, fmt.Errorf("go list -m: %w", err)
	}
	return m, json.Unmarshal(out, &m)
}

func mainModule(dir string) (string, error) {
	m, err := goListModule(dir)
	if err != nil {
		return "", fmt.Errorf("%s is not inside a Go module: %w", dir, err)
	}
	return m.Path, nil
}

func requiresSeedfall(dir, module string) bool {
	if module == seedfallModule {
		return true
	}
	m, err := goListModule(dir, seedfallModule)
	return err == nil && m.Error == nil
}

func mainPackages(dir string, listed []listedPackage, a initArgs) ([]mainPackage, error) {
	workloadDir := filepath.Join(dir, filepath.FromSlash(a.Workload))
	taken := map[string]bool{workloadName: true}
	var mains []mainPackage
	for _, p := range listed {
		if p.DepOnly || p.Standard || p.Name != "main" || p.Dir == workloadDir {
			continue
		}
		rel, err := filepath.Rel(dir, p.Dir)
		if err != nil {
			return nil, err
		}
		m := mainPackage{Pkg: relPkg(rel), ImportPath: p.ImportPath, ImportsNet: slices.Contains(p.Deps, "net"), UsesSim: slices.Contains(p.Deps, simPackage)}
		m.Node = m.ImportsNet
		if len(a.Nodes) > 0 {
			m.Node = slices.ContainsFunc(a.Nodes, m.matches)
		}
		if m.Node {
			m.Name = nodeName(m.Pkg, p.ImportPath, taken)
		}
		mains = append(mains, m)
	}
	for _, n := range a.Nodes {
		if !slices.ContainsFunc(mains, func(m mainPackage) bool { return m.matches(n) }) {
			return nil, fmt.Errorf("node %q is not a main package under %s", n, dir)
		}
	}
	return mains, nil
}

func (m mainPackage) matches(arg string) bool {
	return arg == m.ImportPath || relPkg(filepath.Clean(arg)) == m.Pkg
}

func relPkg(rel string) string {
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "./") {
		return rel
	}
	return "./" + rel
}

func nodeName(pkg, importPath string, taken map[string]bool) string {
	base := path.Base(pkg)
	if pkg == "." {
		base = path.Base(importPath)
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	taken[name] = true
	return name
}

func selected(mains []mainPackage) []mainPackage {
	var out []mainPackage
	for _, m := range mains {
		if m.Node {
			out = append(out, m)
		}
	}
	return out
}

func importPaths(mains []mainPackage) []string {
	out := make([]string, len(mains))
	for i, m := range mains {
		out[i] = m.ImportPath
	}
	return out
}

func packageGraph(listed []listedPackage) map[string]listedPackage {
	graph := map[string]listedPackage{}
	for _, p := range listed {
		graph[p.ImportPath] = p
	}
	return graph
}

func wasip1Blockers(listed []listedPackage, nodes []mainPackage, module string) []blocker {
	return findBlockers(packageGraph(listed), nodes, module, func(p listedPackage) (string, bool) {
		if p.Error == nil {
			return "", false
		}
		return firstLines(p.Error.Err, errorLines), true
	})
}

func cgoPackages(listed []listedPackage, nodes []mainPackage, module string) []blocker {
	return findBlockers(packageGraph(listed), nodes, module, func(p listedPackage) (string, bool) {
		if p.Standard || len(p.CgoFiles) == 0 {
			return "", false
		}
		return "cgo files: " + strings.Join(p.CgoFiles, ", "), true
	})
}

func findBlockers(graph map[string]listedPackage, nodes []mainPackage, module string, bad func(listedPackage) (string, bool)) []blocker {
	out := []blocker{}
	seen := map[string]bool{}
	for _, n := range nodes {
		root := graph[n.ImportPath]
		for _, dep := range append(slices.Clone(root.Deps), root.ImportPath) {
			p, ok := graph[dep]
			if !ok || seen[dep] {
				continue
			}
			reason, isBad := bad(p)
			if !isBad {
				continue
			}
			seen[dep] = true
			b := blocker{Package: dep, Chain: importChain(graph, root.ImportPath, dep), Reason: reason}
			if p.Module != nil {
				b.Module, b.OwnModule = p.Module.Path, p.Module.Path == module
			}
			out = append(out, b)
		}
	}
	return out
}

func importChain(graph map[string]listedPackage, from, to string) []string {
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			var chain []string
			for at := to; at != ""; at = prev[at] {
				chain = append(chain, at)
			}
			slices.Reverse(chain)
			return chain
		}
		for _, next := range graph[cur].Imports {
			if _, ok := prev[next]; !ok {
				prev[next] = cur
				queue = append(queue, next)
			}
		}
	}
	return []string{from, to}
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("(%d more lines)", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

func ms(n int) engine.Duration { return engine.Duration(time.Duration(n) * time.Millisecond) }

func starterConfig(nodes []mainPackage, workloadPkg string) engine.Config {
	cfg := engine.DefaultConfig()
	cfg.Duration = engine.Duration(60 * time.Second)
	cfg.Latency = engine.Range{ms(1), ms(5)}
	cfg.Faults.CrashPerSec = 0.5
	cfg.Faults.RestartAfter = engine.Range{ms(10), ms(500)}
	cfg.Faults.TornWrites = true
	cfg.Faults.CrashOnIO = 0.005
	cfg.Nodes = []engine.NodeSpec{}
	for _, n := range nodes {
		cfg.Nodes = append(cfg.Nodes, engine.NodeSpec{Name: n.Name, Pkg: n.Pkg})
		cfg.NetOverlay = cfg.NetOverlay || !n.UsesSim
	}
	cfg.Workload = engine.NodeSpec{Name: workloadName, Pkg: workloadPkg}
	if len(nodes) > 0 {
		cfg.Workload.Args = []string{"-addr", nodes[0].Name + ":" + guessedPort}
	}
	return cfg
}

func nextSteps(v initView, a initArgs, nodes []mainPackage) []string {
	var steps []string
	if !v.RequiresSeedfall {
		steps = append(steps, "go get "+seedfallModule+"@latest")
	}
	if len(v.Blockers) > 0 {
		steps = append(steps, "fix the wasip1_blockers: upgrade or replace each dependency (a go.mod replace with an in-process shim works, as examples/flockshim does for gofrs/flock), and move platform-specific code in your own packages behind build tags")
	}
	if len(v.Cgo) > 0 {
		steps = append(steps, "cgo is off under wasip1: swap each cgo_packages entry for a pure-Go alternative, or stub it behind a build tag")
	}
	if len(nodes) == 0 {
		steps = append(steps, "no main package imports net: seedfall crashes nodes, not the workload, so add a small main package that serves the code under test (examples/wals/logserver does this for two WAL libraries) and rerun init with -node")
	} else {
		steps = append(steps, fmt.Sprintf("set the workload's -addr in %s to the address %s listens on (%s is a guess); plain net.Listen needs net_overlay, which init turns on when a node does not import %s", a.Config, nodes[0].Name, guessedPort, simPackage))
	}
	steps = append(steps,
		fmt.Sprintf("implement put and get in %s/main.go against your API; until then the sweep fails with \"the workload got acknowledged writes\" never true", a.Workload),
		fmt.Sprintf("seedfall run -c %s -seeds 1-20", a.Config),
	)
	return steps
}

func scaffold(v *initView, a initArgs) error {
	b, err := json.MarshalIndent(v.Config, "", "  ")
	if err != nil {
		return err
	}
	files := []struct {
		rel  string
		data []byte
	}{
		{a.Config, append(b, '\n')},
		{path.Join(a.Workload, "main.go"), workloadSource},
	}
	v.Wrote, v.Kept = []string{}, []string{}
	for _, f := range files {
		full := filepath.Join(v.Dir, filepath.FromSlash(f.rel))
		if _, err := os.Stat(full); err == nil {
			v.Kept = append(v.Kept, f.rel)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, f.data, 0o644); err != nil {
			return err
		}
		v.Wrote = append(v.Wrote, f.rel)
	}
	return nil
}

func printInit(v initView) {
	fmt.Printf("module %s (%s)\n\nmain packages:\n", v.Module, v.Dir)
	if len(v.Mains) == 0 {
		fmt.Println("  none")
	}
	for _, m := range v.Mains {
		role := "skipped"
		if m.Node {
			role = "node " + m.Name
		}
		fmt.Printf("  %-24s %-16s imports net: %-5t uses sim: %t\n", m.Pkg, role, m.ImportsNet, m.UsesSim)
	}
	printBlockers("wasip1 blockers", v.Blockers)
	printBlockers("cgo packages", v.Cgo)
	fmt.Println()
	for _, f := range v.Wrote {
		fmt.Printf("wrote %s\n", f)
	}
	for _, f := range v.Kept {
		fmt.Printf("kept  %s (already exists)\n", f)
	}
	fmt.Println("\nnext:")
	for i, s := range v.Next {
		fmt.Printf("  %d. %s\n", i+1, s)
	}
}

func printBlockers(title string, bs []blocker) {
	fmt.Printf("\n%s: %d\n", title, len(bs))
	for _, b := range bs {
		fmt.Printf("  %s\n    via %s\n", b.Package, strings.Join(b.Chain, " -> "))
		for _, l := range strings.Split(b.Reason, "\n") {
			fmt.Printf("    %s\n", l)
		}
	}
}
