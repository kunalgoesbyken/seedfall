package engine

import (
	"maps"
	"math/rand/v2"
	"path"
	"strings"
)

type nsKind int

const (
	nsCreate nsKind = iota
	nsUnlink
	nsMkdir
	nsRmdir
	nsRename
)

type nsOp struct {
	kind nsKind
	path string
	to   string
	in   *inode
	ino  uint64
}

type namespace struct {
	files   map[string]*inode
	dirs    map[string]uint64
	pending []nsOp
}

func newNamespace(rootIno uint64) *namespace {
	return &namespace{files: map[string]*inode{}, dirs: map[string]uint64{".": rootIno}}
}

func (op nsOp) touches(dir string) bool {
	return path.Dir(op.path) == dir || (op.kind == nsRename && path.Dir(op.to) == dir)
}

func (n *namespace) log(op nsOp) { n.pending = append(n.pending, op) }

func (n *namespace) commit(dir string) {
	var rest []nsOp
	for _, op := range n.pending {
		if op.touches(dir) {
			n.apply(op)
			continue
		}
		rest = append(rest, op)
	}
	n.pending = rest
}

func (n *namespace) reachable(p string) bool {
	for d := path.Dir(p); d != "."; d = path.Dir(d) {
		if _, ok := n.dirs[d]; !ok {
			return false
		}
	}
	return true
}

func (n *namespace) apply(op nsOp) {
	switch op.kind {
	case nsCreate:
		n.files[op.path] = op.in
	case nsUnlink:
		delete(n.files, op.path)
	case nsMkdir:
		n.dirs[op.path] = op.ino
	case nsRmdir:
		delete(n.dirs, op.path)
	case nsRename:
		n.rename(op)
	}
}

func (n *namespace) rename(op nsOp) {
	if op.in != nil {
		delete(n.files, op.path)
		n.files[op.to] = op.in
		return
	}
	moved := func(p string) (string, bool) {
		if p == op.path || strings.HasPrefix(p, op.path+"/") {
			return op.to + strings.TrimPrefix(p, op.path), true
		}
		return "", false
	}
	for _, p := range sortedKeys(n.dirs) {
		if to, ok := moved(p); ok {
			n.dirs[to] = n.dirs[p]
			delete(n.dirs, p)
		}
	}
	for _, p := range sortedKeys(n.files) {
		if to, ok := moved(p); ok {
			n.files[to] = n.files[p]
			delete(n.files, p)
		}
	}
}

func (n *namespace) recover(r *rand.Rand, torn bool) (map[string]*inode, map[string]uint64) {
	if torn && len(n.pending) > 0 {
		for _, op := range n.pending[:r.IntN(len(n.pending)+1)] {
			n.apply(op)
		}
	}
	n.pending = nil
	files, dirs := maps.Clone(n.files), maps.Clone(n.dirs)
	maps.DeleteFunc(dirs, func(p string, _ uint64) bool { return p != "." && !n.reachable(p) })
	maps.DeleteFunc(files, func(p string, _ *inode) bool { return !n.reachable(p) })
	return files, dirs
}
