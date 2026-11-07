package flock

import "sync"

var (
	mu   sync.Mutex
	held = map[string]bool{}
)

type Flock struct {
	path   string
	locked bool
}

func New(path string) *Flock {
	return &Flock{path: path}
}

func (f *Flock) Path() string {
	return f.path
}

func (f *Flock) Locked() bool {
	mu.Lock()
	defer mu.Unlock()
	return f.locked
}

func (f *Flock) TryLock() (bool, error) {
	mu.Lock()
	defer mu.Unlock()
	if f.locked {
		return true, nil
	}
	if held[f.path] {
		return false, nil
	}
	held[f.path] = true
	f.locked = true
	return true, nil
}

func (f *Flock) Unlock() error {
	mu.Lock()
	defer mu.Unlock()
	if f.locked {
		delete(held, f.path)
		f.locked = false
	}
	return nil
}
