package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompilationCacheKeepsRecentEntries(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "wazero-v1.12.0-amd64-linux")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour)
	var oldest string
	for i := range keptCompilations + 3 {
		dir := filepath.Join(root, fmt.Sprintf("%016x", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		at := start.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(dir, at, at); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			oldest = dir
		}
	}
	if _, err := compilationCache(root, [][]byte{[]byte("module")}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != keptCompilations {
		t.Fatalf("cache holds %d entries, want %d", len(entries), keptCompilations)
	}
	for _, gone := range []string{legacy, oldest} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived pruning", filepath.Base(gone))
		}
	}
}
