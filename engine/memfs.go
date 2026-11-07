package engine

import (
	"io"
	"io/fs"
	"math/rand/v2"
	"path"
	"sort"
	"strings"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/sys"
)

type pendingWrite struct {
	off      int64
	data     []byte
	truncate bool
}

type inode struct {
	ino     uint64
	data    []byte
	durable []byte
	synced  bool
	pending []pendingWrite
	mtime   int64
}

type memFS struct {
	fsapi.UnimplementedFS
	clock      func() int64
	mutated    func()
	syncFails  func() bool
	weaken     Mutation
	onWeakened func()
	special    func(string) (fsapi.File, fsapi.Errno, bool)
	nextIno    uint64
	files      map[string]*inode
	dirs       map[string]uint64
	ns         *namespace
}

func newMemFS(clock func() int64) *memFS {
	m := &memFS{clock: clock, files: map[string]*inode{}, dirs: map[string]uint64{}}
	m.dirs["."] = m.allocIno()
	return m
}

func (m *memFS) strict() {
	m.ns = newNamespace(m.dirs["."])
}

func (m *memFS) logNS(op nsOp) {
	if m.ns != nil {
		m.ns.log(op)
	}
}

func (m *memFS) allocIno() uint64 {
	m.nextIno++
	return m.nextIno
}

func clean(p string) string {
	p = path.Clean("/" + p)
	if p == "/" {
		return "."
	}
	return strings.TrimPrefix(p, "/")
}

func (m *memFS) parentExists(p string) bool {
	_, ok := m.dirs[path.Dir(p)]
	return ok
}

func (m *memFS) OpenFile(p string, flag fsapi.Oflag, _ fs.FileMode) (fsapi.File, fsapi.Errno) {
	p = clean(p)
	if m.special != nil {
		if f, errno, ok := m.special(p); ok {
			return f, errno
		}
	}
	if ino, ok := m.dirs[p]; ok {
		if flag&(fsapi.O_WRONLY|fsapi.O_RDWR) != 0 {
			return nil, fsapi.EISDIR
		}
		return &memFile{fs: m, path: p, dirIno: ino}, 0
	}
	in, ok := m.files[p]
	if flag&fsapi.O_DIRECTORY != 0 && ok {
		return nil, fsapi.ENOTDIR
	}
	if flag&fsapi.O_DIRECTORY != 0 {
		return nil, fsapi.ENOENT
	}
	switch {
	case ok && flag&fsapi.O_CREAT != 0 && flag&fsapi.O_EXCL != 0:
		return nil, fsapi.EEXIST
	case !ok && flag&fsapi.O_CREAT == 0:
		return nil, fsapi.ENOENT
	case !ok && !m.parentExists(p):
		return nil, fsapi.ENOENT
	case !ok:
		in = &inode{ino: m.allocIno(), mtime: m.clock()}
		m.files[p] = in
		m.logNS(nsOp{kind: nsCreate, path: p, in: in})
	}
	f := &memFile{fs: m, path: p, in: in, append: flag&fsapi.O_APPEND != 0, sync: flag&(fsapi.O_SYNC|fsapi.O_DSYNC) != 0}
	if flag&fsapi.O_TRUNC != 0 {
		f.Truncate(0)
	}
	return f, 0
}

func (m *memFS) stat(p string) (sys.Stat_t, fsapi.Errno) {
	p = clean(p)
	if ino, ok := m.dirs[p]; ok {
		return dirStat(ino), 0
	}
	if in, ok := m.files[p]; ok {
		return in.stat(), 0
	}
	return sys.Stat_t{}, fsapi.ENOENT
}

func dirStat(ino uint64) sys.Stat_t {
	return sys.Stat_t{Ino: ino, Mode: fs.ModeDir | 0o755, Nlink: 1}
}

func (m *memFS) Stat(p string) (sys.Stat_t, fsapi.Errno)  { return m.stat(p) }
func (m *memFS) Lstat(p string) (sys.Stat_t, fsapi.Errno) { return m.stat(p) }

func (m *memFS) exists(p string) bool {
	_, d := m.dirs[p]
	_, f := m.files[p]
	return d || f
}

func (m *memFS) Mkdir(p string, _ fs.FileMode) fsapi.Errno {
	p = clean(p)
	if m.exists(p) {
		return fsapi.EEXIST
	}
	if !m.parentExists(p) {
		return fsapi.ENOENT
	}
	m.dirs[p] = m.allocIno()
	m.logNS(nsOp{kind: nsMkdir, path: p, ino: m.dirs[p]})
	return 0
}

func (m *memFS) children(dir string) []string {
	var names []string
	for _, p := range append(sortedKeys(m.dirs), sortedKeys(m.files)...) {
		if p != "." && path.Dir(p) == dir {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	return names
}

func (m *memFS) Rmdir(p string) fsapi.Errno {
	p = clean(p)
	if _, ok := m.dirs[p]; !ok {
		return fsapi.ENOENT
	}
	if len(m.children(p)) > 0 {
		return fsapi.ENOTEMPTY
	}
	delete(m.dirs, p)
	m.logNS(nsOp{kind: nsRmdir, path: p})
	return 0
}

func (m *memFS) Unlink(p string) fsapi.Errno {
	p = clean(p)
	if _, ok := m.dirs[p]; ok {
		return fsapi.EISDIR
	}
	if _, ok := m.files[p]; !ok {
		return fsapi.ENOENT
	}
	delete(m.files, p)
	m.logNS(nsOp{kind: nsUnlink, path: p})
	return 0
}

func (m *memFS) Rename(from, to string) fsapi.Errno {
	from, to = clean(from), clean(to)
	if !m.parentExists(to) {
		return fsapi.ENOENT
	}
	if in, ok := m.files[from]; ok {
		delete(m.files, from)
		m.files[to] = in
		m.logNS(nsOp{kind: nsRename, path: from, to: to, in: in})
		return 0
	}
	ino, ok := m.dirs[from]
	if !ok {
		return fsapi.ENOENT
	}
	m.logNS(nsOp{kind: nsRename, path: from, to: to})
	ns := m.ns
	m.ns = nil
	defer func() { m.ns = ns }()
	m.dirs[to] = ino
	for _, c := range m.children(from) {
		if errno := m.Rename(c, to+"/"+path.Base(c)); errno != 0 {
			return errno
		}
	}
	delete(m.dirs, from)
	return 0
}

func (m *memFS) Utimens(string, int64, int64) fsapi.Errno { return 0 }
func (m *memFS) Chmod(string, fs.FileMode) fsapi.Errno    { return 0 }

func (m *memFS) crash(r *rand.Rand, torn bool) {
	if m.ns != nil {
		m.crashStrict(r, torn)
		return
	}
	for _, p := range sortedKeys(m.files) {
		in := m.files[p]
		in.recover(r, torn)
		if !in.synced {
			delete(m.files, p)
		}
	}
}

func (m *memFS) crashStrict(r *rand.Rand, torn bool) {
	seen := map[*inode]bool{}
	for _, files := range []map[string]*inode{m.files, m.ns.files} {
		for _, p := range sortedKeys(files) {
			if in := files[p]; !seen[in] {
				seen[in] = true
				in.recover(r, torn)
			}
		}
	}
	m.files, m.dirs = m.ns.recover(r, torn)
}

func (in *inode) recover(r *rand.Rand, torn bool) {
	data := append([]byte(nil), in.durable...)
	if torn && len(in.pending) > 0 {
		keep := r.IntN(len(in.pending) + 1)
		for i, w := range in.pending[:keep] {
			if i == keep-1 && !w.truncate && len(w.data) > 0 {
				w.data = w.data[:r.IntN(len(w.data)+1)]
			}
			data = w.apply(data)
		}
		in.synced = in.synced || keep > 0
	}
	in.data = data
	in.durable = append([]byte(nil), data...)
	in.pending = nil
}

func (w pendingWrite) apply(data []byte) []byte {
	if w.truncate {
		return resize(data, w.off)
	}
	end := w.off + int64(len(w.data))
	if int64(len(data)) < end {
		data = resize(data, end)
	}
	copy(data[w.off:], w.data)
	return data
}

func resize(data []byte, size int64) []byte {
	if int64(len(data)) >= size {
		return data[:size]
	}
	return append(data, make([]byte, size-int64(len(data)))...)
}

func (in *inode) stat() sys.Stat_t {
	return sys.Stat_t{Ino: in.ino, Mode: 0o644, Nlink: 1, Size: int64(len(in.data)), Mtim: in.mtime, Ctim: in.mtime, Atim: in.mtime}
}

type memFile struct {
	fsapi.UnimplementedFile
	fs      *memFS
	path    string
	in      *inode
	dirIno  uint64
	off     int64
	append  bool
	sync    bool
	dirRead int
}

func (f *memFile) IsDir() (bool, fsapi.Errno) { return f.in == nil, 0 }
func (f *memFile) IsAppend() bool             { return f.append }

func (f *memFile) SetAppend(enable bool) fsapi.Errno {
	f.append = enable
	return 0
}

func (f *memFile) Ino() (sys.Inode, fsapi.Errno) {
	if f.in == nil {
		return f.dirIno, 0
	}
	return f.in.ino, 0
}

func (f *memFile) Stat() (sys.Stat_t, fsapi.Errno) {
	if f.in == nil {
		return dirStat(f.dirIno), 0
	}
	return f.in.stat(), 0
}

func (f *memFile) Read(buf []byte) (int, fsapi.Errno) {
	n, errno := f.Pread(buf, f.off)
	f.off += int64(n)
	return n, errno
}

func (f *memFile) Pread(buf []byte, off int64) (int, fsapi.Errno) {
	if f.in == nil {
		return 0, fsapi.EISDIR
	}
	if off >= int64(len(f.in.data)) {
		return 0, 0
	}
	return copy(buf, f.in.data[off:]), 0
}

func (f *memFile) Seek(offset int64, whence int) (int64, fsapi.Errno) {
	if f.in == nil {
		if offset == 0 && whence == io.SeekStart {
			f.dirRead = 0
			return 0, 0
		}
		return 0, fsapi.EINVAL
	}
	var base int64
	switch whence {
	case io.SeekCurrent:
		base = f.off
	case io.SeekEnd:
		base = int64(len(f.in.data))
	}
	if base+offset < 0 {
		return 0, fsapi.EINVAL
	}
	f.off = base + offset
	return f.off, 0
}

func (f *memFile) Readdir(n int) ([]fsapi.Dirent, fsapi.Errno) {
	if f.in != nil {
		return nil, fsapi.ENOTDIR
	}
	names := f.fs.children(f.path)[f.dirRead:]
	if n > 0 && n < len(names) {
		names = names[:n]
	}
	f.dirRead += len(names)
	out := make([]fsapi.Dirent, 0, len(names))
	for _, p := range names {
		st, _ := f.fs.stat(p)
		out = append(out, fsapi.Dirent{Ino: st.Ino, Name: path.Base(p), Type: st.Mode.Type()})
	}
	return out, 0
}

func (f *memFile) Write(buf []byte) (int, fsapi.Errno) {
	if f.append && f.in != nil {
		f.off = int64(len(f.in.data))
	}
	n, errno := f.Pwrite(buf, f.off)
	f.off += int64(n)
	return n, errno
}

func (f *memFile) Pwrite(buf []byte, off int64) (int, fsapi.Errno) {
	if f.in == nil {
		return 0, fsapi.EISDIR
	}
	if errno := f.record(pendingWrite{off: off, data: append([]byte(nil), buf...)}); errno != 0 {
		return 0, errno
	}
	return len(buf), 0
}

func (f *memFile) Truncate(size int64) fsapi.Errno {
	if f.in == nil {
		return fsapi.EISDIR
	}
	return f.record(pendingWrite{off: size, truncate: true})
}

func (f *memFile) record(w pendingWrite) fsapi.Errno {
	f.in.data = w.apply(f.in.data)
	f.in.pending = append(f.in.pending, w)
	f.in.mtime = f.fs.clock()
	f.fs.notify()
	if f.sync && !f.weakenedTo(OsyncIgnored) {
		return f.Sync()
	}
	return 0
}

func (m *memFS) notify() {
	if m.mutated != nil {
		m.mutated()
	}
}

func (f *memFile) Sync() fsapi.Errno {
	if f.in == nil {
		f.fs.notify()
		if f.fs.ns != nil && !f.weakenedTo(DirsyncNoop) {
			f.fs.ns.commit(f.path)
		}
		return 0
	}
	f.fs.notify()
	if f.fs.syncFails != nil && f.fs.syncFails() {
		f.in.pending = nil
		return fsapi.EIO
	}
	if f.weakenedTo(FsyncNoop) {
		return 0
	}
	for _, w := range f.in.pending {
		f.in.durable = w.apply(f.in.durable)
	}
	f.in.pending = nil
	f.in.synced = true
	return 0
}

func (f *memFile) Datasync() fsapi.Errno            { return f.Sync() }
func (f *memFile) Utimens(int64, int64) fsapi.Errno { return 0 }
func (f *memFile) Close() fsapi.Errno               { return 0 }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
