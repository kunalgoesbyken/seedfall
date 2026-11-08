package engine

import (
	"bytes"
	"slices"

	"github.com/tetratelabs/wazero/api"
)

const (
	mmapShared = 1 << iota
	mmapWritable
)

const msyncSync = 1

const (
	mmapOK = int32(iota)
	mmapBadFile
	mmapFault
	mmapIOError
)

type mapping struct {
	file     *memFile
	off      int64
	ptr      uint32
	size     uint32
	writable bool
	mem      api.Memory
}

type dirtyPage struct {
	in   *inode
	path string
	off  int64
	data []byte
}

func (m *memFS) inodeOf(ino uint64) (*inode, string, bool) {
	for _, p := range sortedKeys(m.files) {
		if in := m.files[p]; in.ino == ino {
			return in, p, true
		}
	}
	return nil, "", false
}

func (m *memFS) mmap(mem api.Memory, ino uint64, ptr, size uint32, off int64, flags int32) int32 {
	in, p, ok := m.inodeOf(ino)
	if !ok {
		return mmapBadFile
	}
	m.call("mmap", p, int64(size))
	buf, ok := mem.Read(ptr, size)
	if !ok {
		return mmapFault
	}
	if off < int64(len(in.data)) {
		copy(buf, in.data[off:])
	}
	if flags&mmapShared != 0 {
		m.maps = append(m.maps, &mapping{file: &memFile{fs: m, path: p, in: in}, off: off, ptr: ptr, size: size, writable: flags&mmapWritable != 0, mem: mem})
	}
	return mmapOK
}

func (m *memFS) mappingAt(ptr uint32) *mapping {
	for _, mm := range m.maps {
		if ptr >= mm.ptr && ptr < mm.ptr+mm.size {
			return mm
		}
	}
	return nil
}

func (m *memFS) msync(ptr, size uint32, flags int32) int32 {
	mm := m.mappingAt(ptr)
	if mm == nil {
		return mmapOK
	}
	m.call("msync", mm.file.path, int64(size))
	start := mm.off + int64(ptr-mm.ptr)
	mm.flush(start, start+int64(size))
	if flags&msyncSync == 0 {
		return mmapOK
	}
	if errno := mm.file.Sync(); errno != 0 {
		return mmapIOError
	}
	return mmapOK
}

func (m *memFS) munmap(ptr uint32) int32 {
	mm := m.mappingAt(ptr)
	if mm == nil {
		return mmapOK
	}
	m.call("munmap", mm.file.path, int64(mm.size))
	mm.flush(mm.off, mm.off+int64(mm.size))
	m.maps = slices.DeleteFunc(m.maps, func(x *mapping) bool { return x == mm })
	return mmapOK
}

func (mm *mapping) dirty(from, to int64) []pendingWrite {
	if !mm.writable {
		return nil
	}
	buf, ok := mm.mem.Read(mm.ptr, mm.size)
	if !ok {
		return nil
	}
	data := mm.file.in.data
	var out []pendingWrite
	for o := int64(0); o < int64(mm.size); o += pageSize {
		at := mm.off + o
		end := min(at+pageSize, mm.off+int64(mm.size), int64(len(data)))
		if at >= end || at < from || at >= to {
			continue
		}
		if span := buf[o : o+end-at]; !bytes.Equal(span, data[at:end]) {
			out = append(out, pendingWrite{off: at, data: slices.Clone(span)})
		}
	}
	return out
}

func (mm *mapping) flush(from, to int64) {
	dirty := mm.dirty(from, to)
	for _, w := range dirty {
		mm.file.store(w)
	}
	if len(dirty) > 0 {
		mm.file.fs.notify()
	}
}

func (m *memFS) flushMapped(in *inode) {
	for _, mm := range m.maps {
		if mm.file.in == in {
			for _, w := range mm.dirty(mm.off, mm.off+int64(mm.size)) {
				mm.file.store(w)
			}
		}
	}
}

func (m *memFS) refreshMapped(in *inode, w pendingWrite) {
	if w.truncate {
		return
	}
	for _, mm := range m.maps {
		if mm.file.in != in {
			continue
		}
		end := min(w.off+int64(len(w.data)), mm.off+int64(mm.size))
		if start := max(w.off, mm.off); start < end {
			if buf, ok := mm.mem.Read(mm.ptr, mm.size); ok {
				copy(buf[start-mm.off:end-mm.off], w.data[start-w.off:end-w.off])
			}
		}
	}
}

func (m *memFS) endInstance(killed bool) {
	for _, mm := range m.maps {
		for _, w := range mm.dirty(mm.off, mm.off+int64(mm.size)) {
			if killed {
				m.crashDirty = append(m.crashDirty, dirtyPage{mm.file.in, mm.file.path, w.off, w.data})
			} else {
				mm.file.store(w)
			}
		}
	}
	m.maps = nil
}

func (m *memFS) earlyWriteback(c crashModel) {
	for _, d := range m.crashDirty {
		if c.mapped != nil && c.mapped(d.path, d.off) {
			d.in.keepPage(d.off, d.data)
		}
	}
	m.crashDirty = nil
}

func (in *inode) keepPage(off int64, data []byte) {
	if off >= int64(len(in.durable)) {
		return
	}
	copy(in.durable[off:], data)
	in.synced = true
}
