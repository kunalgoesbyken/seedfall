package engine

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

const mutantSource = `package main

func save(f file, n int) error {
	if n >= limit {
		return nil
	}
	if err := f.Write(n); err != nil {
		return err
	}
	return f.Sync()
}
`

func TestFileMutantsFindComparisonsErrorReturnsAndSyncs(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mutantSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range fileMutants(fset, file, []byte(mutantSource)) {
		mutated := mutantSource[:m.start] + m.with + mutantSource[m.end:]
		if _, err := parser.ParseFile(token.NewFileSet(), "main.go", mutated, 0); err != nil {
			t.Fatalf("%s does not parse: %v", m.Op, err)
		}
		got = append(got, m.Op+": "+m.From+" -> "+m.To)
	}
	want := []string{
		"flip_comparison: n >= limit -> n < limit",
		"skip_error_return: err != nil -> false && err != nil",
		"flip_comparison: err != nil -> err == nil",
		"skip_sync: f.Sync() -> func() error { _ = f; return nil }()",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutants %q, want %q", got, want)
	}
}

func TestSelectAppMutantsIsSeededAndOrdered(t *testing.T) {
	var all []AppMutant
	for i := range 50 {
		all = append(all, AppMutant{file: "a.go", start: i})
	}
	a, b := SelectAppMutants(all, 5, 7), SelectAppMutants(all, 5, 7)
	if len(a) != 5 || !reflect.DeepEqual(a, b) || !slices.IsSortedFunc(a, func(x, y AppMutant) int { return x.start - y.start }) {
		t.Fatalf("selection %+v", a)
	}
	if reflect.DeepEqual(a, SelectAppMutants(all, 5, 8)) {
		t.Fatal("two seeds picked the same mutants")
	}
	if len(SelectAppMutants(all, 0, 7)) != 50 || len(SelectAppMutants(all, 99, 7)) != 50 {
		t.Fatal("a count of 0 or more than the candidates does not pick them all")
	}
}

func TestWithOverlayMergesTheNetOverlay(t *testing.T) {
	dir := t.TempDir()
	net := filepath.Join(dir, "net.json")
	b, _ := json.Marshal(map[string]any{"Replace": map[string]string{"/goroot/net/fd_fake.go": "/o/fd_fake.go"}})
	os.WriteFile(net, b, 0o644)
	flags, err := withOverlay([]string{"-modfile", "m.mod", "-overlay", net}, dir, map[string]string{"/src/main.go": "/m/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 4 || flags[0] != "-modfile" || flags[2] != "-overlay" {
		t.Fatalf("flags %q", flags)
	}
	var merged struct{ Replace map[string]string }
	raw, _ := os.ReadFile(flags[3])
	json.Unmarshal(raw, &merged)
	if len(merged.Replace) != 2 || merged.Replace["/src/main.go"] != "/m/main.go" {
		t.Fatalf("overlay %v", merged.Replace)
	}
}
