package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestEveryExampleConfigLoadsWithEveryProfile(t *testing.T) {
	loaded := 0
	err := filepath.WalkDir("../examples", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc map[string]any
		if decodeJSON(b, &doc) != nil || doc["workload"] == nil && doc["workloads"] == nil && doc["extends"] == nil {
			return nil
		}
		layers, err := readLayers(path, nil)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		profiles, _ := layers["profiles"].(map[string]any)
		for _, name := range append([]string{""}, sortedKeys(profiles)...) {
			ref := ConfigRef{Path: path, Profile: name}
			if _, err := Load(ref); err != nil {
				t.Errorf("%s: %v", ref, err)
			}
			loaded++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded < 100 {
		t.Fatalf("loaded %d example configs, expected the examples directory to hold many more", loaded)
	}
}
