package kv

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	logExt   = ".log"
	snapExt  = ".snap"
	tmpExt   = ".tmp"
	lockName = "LOCK"
)

type fileKind int

const (
	kindLog fileKind = iota
	kindSnap
)

type dataFile struct {
	id   uint64
	kind fileKind
}

func fileName(id uint64, kind fileKind) string {
	ext := logExt
	if kind == kindSnap {
		ext = snapExt
	}
	return fmt.Sprintf("%020d%s", id, ext)
}

func parseFileName(name string) (dataFile, bool) {
	var kind fileKind
	var stem string
	switch {
	case strings.HasSuffix(name, logExt):
		kind, stem = kindLog, strings.TrimSuffix(name, logExt)
	case strings.HasSuffix(name, snapExt):
		kind, stem = kindSnap, strings.TrimSuffix(name, snapExt)
	default:
		return dataFile{}, false
	}
	if len(stem) != 20 {
		return dataFile{}, false
	}
	id, err := strconv.ParseUint(stem, 10, 64)
	if err != nil || id == 0 {
		return dataFile{}, false
	}
	return dataFile{id: id, kind: kind}, true
}

func listDir(dir string) (files []dataFile, tmps []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, tmpExt) {
			tmps = append(tmps, filepath.Join(dir, name))
			continue
		}
		if df, ok := parseFileName(name); ok {
			files = append(files, df)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].id < files[j].id })
	return files, tmps, nil
}

var (
	openDirsMu sync.Mutex
	openDirs   = map[string]struct{}{}
)

func registerDir(dir string) bool {
	openDirsMu.Lock()
	defer openDirsMu.Unlock()
	if _, ok := openDirs[dir]; ok {
		return false
	}
	openDirs[dir] = struct{}{}
	return true
}

func unregisterDir(dir string) {
	openDirsMu.Lock()
	defer openDirsMu.Unlock()
	delete(openDirs, dir)
}
