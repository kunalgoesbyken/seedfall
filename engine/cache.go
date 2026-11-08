package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tetratelabs/wazero"
)

const (
	keptCompilations = 8
	cacheKeyLen      = 16
)

func compilationCache(root string, modules [][]byte) (wazero.CompilationCache, error) {
	h := sha256.New()
	for _, m := range modules {
		h.Write(m)
	}
	dir := filepath.Join(root, hex.EncodeToString(h.Sum(nil))[:cacheKeyLen])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now()
	os.Chtimes(dir, now, now)
	pruneCache(root, keptCompilations)
	return wazero.NewCompilationCacheWithDir(dir)
}

func pruneCache(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type entry struct {
		path string
		used time.Time
	}
	var all []entry
	for _, e := range entries {
		if !isCacheKey(e.Name()) {
			os.RemoveAll(filepath.Join(root, e.Name()))
			continue
		}
		if info, err := e.Info(); err == nil {
			all = append(all, entry{filepath.Join(root, e.Name()), info.ModTime()})
		}
	}
	slices.SortFunc(all, func(a, b entry) int { return b.used.Compare(a.used) })
	for _, e := range all[min(keep, len(all)):] {
		os.RemoveAll(e.path)
	}
}

func isCacheKey(name string) bool {
	_, err := hex.DecodeString(name)
	return len(name) == cacheKeyLen && err == nil
}
