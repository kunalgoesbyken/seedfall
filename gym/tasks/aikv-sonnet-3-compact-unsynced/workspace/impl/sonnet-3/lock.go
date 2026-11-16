package kv

import (
	"path/filepath"
	"sync"
)

const lockName = "LOCK"

var (
	openMu   sync.Mutex
	openDirs = map[string]struct{}{}
)

func acquireDir(dir string) (func(), error) {
	openMu.Lock()
	defer openMu.Unlock()
	if _, busy := openDirs[dir]; busy {
		return nil, ErrLocked
	}
	unlock, err := lockFile(filepath.Join(dir, lockName))
	if err != nil {
		return nil, err
	}
	openDirs[dir] = struct{}{}
	return func() {
		openMu.Lock()
		defer openMu.Unlock()
		_ = unlock()
		delete(openDirs, dir)
	}, nil
}
