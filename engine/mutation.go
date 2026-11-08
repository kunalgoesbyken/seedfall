package engine

import (
	"fmt"
	"slices"
)

type Mutation string

const (
	NoMutation    Mutation = ""
	FsyncNoop     Mutation = "fsync_noop"
	DirsyncNoop   Mutation = "dirsync_noop"
	OsyncIgnored  Mutation = "osync_ignored"
	FsyncCorrupts Mutation = "fsync_corrupts"
)

var Mutations = []Mutation{FsyncNoop, DirsyncNoop, OsyncIgnored, FsyncCorrupts}

func (m Mutation) Describe() string {
	switch m {
	case FsyncNoop:
		return "file fsync returns success without making anything durable"
	case DirsyncNoop:
		return "directory fsync does nothing (matters with strict_dirs)"
	case OsyncIgnored:
		return "writes to O_SYNC and O_DSYNC files are no longer synchronous"
	case FsyncCorrupts:
		return "file fsync makes the data durable with one bit flipped, so it reads back wrong after a crash"
	}
	return "no mutation"
}

func ParseMutation(s string) (Mutation, error) {
	m := Mutation(s)
	if m == NoMutation || slices.Contains(Mutations, m) {
		return m, nil
	}
	return NoMutation, fmt.Errorf("unknown mutation %q", s)
}

func (r *Runner) Weakened() int64 { return r.weakened.Load() }

func (f *memFile) weakenedTo(m Mutation) bool {
	if f.fs.weaken != m {
		return false
	}
	if f.fs.onWeakened != nil {
		f.fs.onWeakened()
	}
	return true
}
