package engine

import (
	"fmt"
	"slices"
	"strings"
)

type Mutation string

const (
	NoMutation         Mutation = ""
	FsyncNoop          Mutation = "fsync_noop"
	DirsyncNoop        Mutation = "dirsync_noop"
	OsyncIgnored       Mutation = "osync_ignored"
	FsyncCorrupts      Mutation = "fsync_corrupts"
	RenameNotAtomic    Mutation = "rename_not_atomic"
	TruncateNotDurable Mutation = "truncate_not_durable"
	NetDropAfterClose  Mutation = "net_drop_after_close"
	NetDupChunk        Mutation = "net_dup_chunk"
	MonotonicJump      Mutation = "monotonic_jump"
)

const dupChunkProb = 0.05

var Mutations = []Mutation{FsyncNoop, DirsyncNoop, OsyncIgnored, FsyncCorrupts, RenameNotAtomic, TruncateNotDurable, NetDropAfterClose, NetDupChunk, MonotonicJump}

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
	case RenameNotAtomic:
		return "a rename over an existing file is not durable until the next fsync on that disk, and a crash before it loses both names"
	case TruncateNotDurable:
		return "fsync makes writes durable but not truncations, so a crash brings back the old length and the bytes past it"
	case NetDropAfterClose:
		return "closing a connection silently drops the last chunk written to it that the peer has not read yet"
	case NetDupChunk:
		return "a seeded 5% of connection writes are delivered twice, which TCP never does"
	case MonotonicJump:
		return "clock jumps move the monotonic clock too, so it can run backwards or leap ahead"
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

func ParseMutations(list string) ([]Mutation, error) {
	if list == "" {
		return Mutations, nil
	}
	var out []Mutation
	for _, s := range strings.Split(list, ",") {
		m, err := ParseMutation(strings.TrimSpace(s))
		if err != nil || m == NoMutation {
			return nil, fmt.Errorf("unknown mutation %q, want one of %s", s, mutationNames())
		}
		out = append(out, m)
	}
	return out, nil
}

func mutationNames() string {
	names := make([]string, len(Mutations))
	for i, m := range Mutations {
		names[i] = string(m)
	}
	return strings.Join(names, ", ")
}

func (r *Runner) Weakened() int64 { return r.weakened.Load() }

func (f *memFile) weakenedTo(m Mutation) bool { return f.fs.weakenedTo(m) }

func (m *memFS) weakenedTo(mut Mutation) bool {
	if m.weaken != mut {
		return false
	}
	if m.onWeakened != nil {
		m.onWeakened()
	}
	return true
}

func (w *world) weakenedTo(m Mutation) bool {
	if w.mutation != m {
		return false
	}
	w.weakened()
	return true
}

func (m *memFS) renameNotDurable(from, to string) {
	if _, replaced := m.files[to]; replaced && m.weakenedTo(RenameNotAtomic) {
		m.unsafeRenames = append(m.unsafeRenames, from, to)
	}
}

func (m *memFS) loseUnsafeRenames() {
	for _, p := range m.unsafeRenames {
		delete(m.files, p)
		if m.ns != nil {
			delete(m.ns.files, p)
		}
	}
	m.unsafeRenames = nil
}

func (f *memFile) durablePending() []pendingWrite {
	shrinks := func(w pendingWrite) bool { return w.truncate && w.off < int64(len(f.in.durable)) }
	if !slices.ContainsFunc(f.in.pending, shrinks) || !f.weakenedTo(TruncateNotDurable) {
		return f.in.pending
	}
	return slices.DeleteFunc(slices.Clone(f.in.pending), func(w pendingWrite) bool { return w.truncate })
}

func (n *network) dropLastChunk(ep *endpoint) {
	if !ep.peer.owner.node.crashable {
		return
	}
	inbox := ep.peer.inbox
	for i := len(inbox) - 1; i >= 0; i-- {
		if inbox[i].kind != chunkData || len(inbox[i].data) == 0 {
			continue
		}
		if n.w.weakenedTo(NetDropAfterClose) {
			ep.peer.inbox = slices.Delete(inbox, i, i+1)
		}
		return
	}
}

func (w *world) duplicates(to *node, kind chunkKind) bool {
	return kind == chunkData && to.crashable && w.mutation == NetDupChunk && w.mutantRand.Float64() < dupChunkProb && w.weakenedTo(NetDupChunk)
}
