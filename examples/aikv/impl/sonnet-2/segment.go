package kv

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type segKind int

const (
	kindSeg segKind = iota
	kindCmp
)

var segExt = map[segKind]string{
	kindSeg: ".seg",
	kindCmp: ".cmp",
}

type segment struct {
	id   uint64
	kind segKind
	f    *os.File
	size int64
	path string
}

func (s *segment) name() string {
	return filepath.Base(s.path)
}

type fileInfo struct {
	id   uint64
	kind segKind
}

func segPath(dir string, id uint64, kind segKind) string {
	return filepath.Join(dir, fmt.Sprintf("%016d%s", id, segExt[kind]))
}

func createFile(dir string, id uint64, kind segKind) (*os.File, error) {
	return os.OpenFile(segPath(dir, id, kind), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
}

func listFiles(dir string) ([]fileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for kind, ext := range segExt {
			base, ok := strings.CutSuffix(e.Name(), ext)
			if !ok {
				continue
			}
			id, err := strconv.ParseUint(base, 10, 64)
			if err != nil || id == 0 {
				continue
			}
			files = append(files, fileInfo{id: id, kind: kind})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].id < files[j].id })
	for i := 1; i < len(files); i++ {
		if files[i].id == files[i-1].id {
			return nil, fmt.Errorf("%w: duplicate segment id %d", ErrCorrupt, files[i].id)
		}
	}
	return files, nil
}
