package engine

import (
	"io"
	"io/fs"
	"maps"
	"math/rand/v2"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/sys"
)

const (
	pageSize    = 4096
	maxSymlinks = 40
)

type crashModel struct {
	torn    bool
	model   string
	pages   *rand.Rand
	corrupt *rand.Rand
}

type pendingWrite struct {
	off      int64
	data     []byte
	truncate bool
	clip     bool
}

type inode struct {
	ino     uint64
	data    []byte
	durable []byte
	synced  bool
	pending []pendingWrite
	target  string
	nlink   uint64
	mtime   int64
	atime   int64
	saved   [2]int64
	tear    *Tear
	wb      *Writeback
	damage  []Damage
	stale   map[int64]bool
}

type memFS struct {
	fsapi.UnimplementedFS
	clock         func() int64
	mutated       func()
	syncFails     func() bool
	writeFails    func(path string) fsapi.Errno
	readFault     func(path string, off int64, n int) (fsapi.Errno, int, byte)
	syncDamage    func(path string, dirty []int64, pages int64) (Damage, bool)
	syncWait      func()
	diskFull      func(path string, kept, size int)
	capacity      int64
	noSpace       bool
	weaken        Mutation
	onWeakened    func()
	special       func(string) (fsapi.File, fsapi.Errno, bool)
	observe       func(op, path string, arg int64)
	unsafeRenames []string
	nextIno       uint64
	files         map[string]*inode
	dirs          map[string]uint64
	ns            *namespace
}

func newMemFS(clock func() int64) *memFS {
	m := &memFS{clock: clock, files: map[string]*inode{}, dirs: map[string]uint64{}}
	m.dirs["."] = m.allocIno()
	return m
}

func (m *memFS) call(op, p string, arg int64) {
	if m.observe != nil {
		m.observe(op, p, arg)
	}
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

func (m *memFS) newInode() *inode {
	now := m.clock()
	return &inode{ino: m.allocIno(), nlink: 1, mtime: now, atime: now, saved: [2]int64{now, now}}
}

func (m *memFS) resolve(p string, follow bool) (string, fsapi.Errno) {
	p = clean(p)
	for hops := 0; ; hops++ {
		link, rest, ok := m.firstSymlink(p, follow)
		if !ok {
			return p, 0
		}
		if hops == maxSymlinks {
			return "", fsapi.ELOOP
		}
		target := m.files[link].target
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(link), target)
		}
		p = clean(path.Join(target, rest))
	}
}

func (m *memFS) firstSymlink(p string, follow bool) (string, string, bool) {
	parts := strings.Split(p, "/")
	for i := range parts {
		at := strings.Join(parts[:i+1], "/")
		if in, ok := m.files[at]; ok && in.target != "" && (follow || i < len(parts)-1) {
			return at, strings.Join(parts[i+1:], "/"), true
		}
	}
	return "", "", false
}

func (m *memFS) OpenFile(p string, flag fsapi.Oflag, _ fs.FileMode) (fsapi.File, fsapi.Errno) {
	p = clean(p)
	m.call("path_open", p, int64(flag))
	if m.special != nil {
		if f, errno, ok := m.special(p); ok {
			return f, errno
		}
	}
	p, errno := m.resolve(p, flag&fsapi.O_NOFOLLOW == 0)
	if errno != 0 {
		return nil, errno
	}
	if in, ok := m.files[p]; ok && in.target != "" {
		return nil, fsapi.ELOOP
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
		in = m.newInode()
		m.files[p] = in
		m.logNS(nsOp{kind: nsCreate, path: p, in: in})
	}
	f := &memFile{fs: m, path: p, in: in, append: flag&fsapi.O_APPEND != 0, sync: flag&(fsapi.O_SYNC|fsapi.O_DSYNC) != 0}
	if flag&fsapi.O_TRUNC != 0 {
		f.Truncate(0)
	}
	return f, 0
}

func (m *memFS) stat(op, p string, follow bool) (sys.Stat_t, fsapi.Errno) {
	p = clean(p)
	m.call(op, p, 0)
	p, errno := m.resolve(p, follow)
	if errno != 0 {
		return sys.Stat_t{}, errno
	}
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

func (m *memFS) Stat(p string) (sys.Stat_t, fsapi.Errno)  { return m.stat("path_stat", p, true) }
func (m *memFS) Lstat(p string) (sys.Stat_t, fsapi.Errno) { return m.stat("path_lstat", p, false) }

func (m *memFS) exists(p string) bool {
	_, d := m.dirs[p]
	_, f := m.files[p]
	return d || f
}

func (m *memFS) Mkdir(p string, _ fs.FileMode) fsapi.Errno {
	p = clean(p)
	m.call("path_mkdir", p, 0)
	p, errno := m.resolve(p, false)
	if errno != 0 {
		return errno
	}
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
	m.call("path_rmdir", p, 0)
	p, errno := m.resolve(p, false)
	if errno != 0 {
		return errno
	}
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
	m.call("path_unlink", p, 0)
	p, errno := m.resolve(p, false)
	if errno != 0 {
		return errno
	}
	if _, ok := m.dirs[p]; ok {
		return fsapi.EISDIR
	}
	in, ok := m.files[p]
	if !ok {
		return fsapi.ENOENT
	}
	in.nlink--
	delete(m.files, p)
	m.logNS(nsOp{kind: nsUnlink, path: p})
	return 0
}

func (m *memFS) Rename(from, to string) fsapi.Errno {
	from, to = clean(from), clean(to)
	m.call("path_rename", from+" "+to, 0)
	from, errno := m.resolve(from, false)
	if errno != 0 {
		return errno
	}
	if to, errno = m.resolve(to, false); errno != 0 {
		return errno
	}
	if !m.parentExists(to) {
		return fsapi.ENOENT
	}
	if in, ok := m.files[from]; ok {
		replaced, exists := m.files[to]
		if exists && replaced == in {
			return 0
		}
		m.renameNotDurable(from, to)
		if exists {
			replaced.nlink--
		}
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

func (m *memFS) Link(oldPath, newPath string) fsapi.Errno {
	oldPath, newPath = clean(oldPath), clean(newPath)
	m.call("path_link", oldPath+" "+newPath, 0)
	from, errno := m.resolve(oldPath, false)
	if errno != 0 {
		return errno
	}
	to, errno := m.resolve(newPath, false)
	if errno != 0 {
		return errno
	}
	if _, ok := m.dirs[from]; ok {
		return fsapi.EPERM
	}
	in, ok := m.files[from]
	switch {
	case !ok || !m.parentExists(to):
		return fsapi.ENOENT
	case m.exists(to):
		return fsapi.EEXIST
	}
	in.nlink++
	m.files[to] = in
	m.logNS(nsOp{kind: nsCreate, path: to, in: in})
	return 0
}

func (m *memFS) Symlink(target, link string) fsapi.Errno {
	link = clean(link)
	m.call("path_symlink", link, 0)
	to, errno := m.resolve(link, false)
	if errno != 0 {
		return errno
	}
	if m.exists(to) {
		return fsapi.EEXIST
	}
	if !m.parentExists(to) {
		return fsapi.ENOENT
	}
	in := m.newInode()
	in.target, in.synced = target, true
	m.files[to] = in
	m.logNS(nsOp{kind: nsCreate, path: to, in: in})
	return 0
}

func (m *memFS) Readlink(p string) (string, fsapi.Errno) {
	p = clean(p)
	m.call("path_readlink", p, 0)
	p, errno := m.resolve(p, false)
	if errno != 0 {
		return "", errno
	}
	in, ok := m.files[p]
	if !ok {
		if _, isDir := m.dirs[p]; isDir {
			return "", fsapi.EINVAL
		}
		return "", fsapi.ENOENT
	}
	if in.target == "" {
		return "", fsapi.EINVAL
	}
	return in.target, 0
}

func (m *memFS) Utimens(p string, atim, mtim int64) fsapi.Errno {
	p = clean(p)
	m.call("path_utimens", p, mtim)
	p, errno := m.resolve(p, true)
	if errno != 0 {
		return errno
	}
	if in, ok := m.files[p]; ok {
		in.setTimes(atim, mtim)
		return 0
	}
	if _, ok := m.dirs[p]; ok {
		return 0
	}
	return fsapi.ENOENT
}

func (in *inode) setTimes(atim, mtim int64) {
	if atim != fsapi.UTIME_OMIT {
		in.atime = atim
	}
	if mtim != fsapi.UTIME_OMIT {
		in.mtime = mtim
	}
}

func (m *memFS) Chmod(string, fs.FileMode) fsapi.Errno { return 0 }

type fileDamage struct {
	path string
	Damage
}

func (m *memFS) crash(r *rand.Rand, c crashModel) []fileDamage {
	hit := map[*inode]Damage{}
	if m.ns != nil {
		m.crashStrict(r, c, hit)
	} else {
		m.crashLoose(r, c, hit)
	}
	m.loseUnsafeRenames()
	m.recount()
	var damaged []fileDamage
	for _, p := range sortedKeys(m.files) {
		if d, ok := hit[m.files[p]]; ok {
			damaged = append(damaged, fileDamage{p, d})
			delete(hit, m.files[p])
		}
	}
	return damaged
}

func (m *memFS) crashLoose(r *rand.Rand, c crashModel, hit map[*inode]Damage) {
	at := m.crashTime()
	for _, p := range sortedKeys(m.files) {
		in := m.files[p]
		if d, ok := in.recover(r, c, at); ok {
			hit[in] = d
		}
		if !in.synced {
			delete(m.files, p)
		}
	}
}

func (m *memFS) crashStrict(r *rand.Rand, c crashModel, hit map[*inode]Damage) {
	seen := map[*inode]bool{}
	at := m.crashTime()
	for _, files := range []map[string]*inode{m.files, m.ns.files} {
		for _, p := range sortedKeys(files) {
			if in := files[p]; !seen[in] {
				seen[in] = true
				if d, ok := in.recover(r, c, at); ok {
					hit[in] = d
				}
			}
		}
	}
	m.files, m.dirs = m.ns.recover(r, c)
}

func (m *memFS) recount() {
	for _, in := range m.files {
		in.nlink = 0
	}
	for _, in := range m.files {
		in.nlink++
	}
}

func (m *memFS) crashTime() time.Duration { return time.Duration(m.clock() - epochNanos) }

func (in *inode) recover(r *rand.Rand, c crashModel, at time.Duration) (Damage, bool) {
	pending := in.pending
	data := append([]byte(nil), in.durable...)
	switch {
	case c.pages != nil && len(in.pending) > 0:
		data = in.writeBack(c, at, data)
	case c.torn && len(in.pending) > 0:
		keep := r.IntN(len(in.pending) + 1)
		for i, w := range in.pending[:keep] {
			if i == keep-1 && !w.truncate && len(w.data) > 0 {
				w = in.tearWrite(r, w, at)
			}
			data = w.apply(data)
		}
		in.synced = in.synced || keep > 0
	}
	in.data = data
	in.durable = append([]byte(nil), data...)
	in.pending = nil
	in.stale = nil
	in.atime, in.mtime = in.saved[0], in.saved[1]
	if c.corrupt == nil {
		return Damage{}, false
	}
	return in.corruptWritten(c.corrupt, pending, at)
}

func (in *inode) corruptWritten(r *rand.Rand, pending []pendingWrite, at time.Duration) (Damage, bool) {
	var spans []ByteRange
	var total int64
	for _, w := range pending {
		if end := min(w.off+int64(len(w.data)), int64(len(in.durable))); !w.truncate && end > w.off {
			spans = append(spans, ByteRange{Start: w.off, End: end})
			total += end - w.off
		}
	}
	if total == 0 {
		return Damage{}, false
	}
	i := r.Int64N(total)
	for _, s := range spans {
		if i < s.End-s.Start {
			return in.flip(s.Start+i, byte(1)<<r.IntN(8), DamageCrash, at), true
		}
		i -= s.End - s.Start
	}
	return Damage{}, false
}

func (in *inode) flip(off int64, mask byte, kind string, at time.Duration) Damage {
	if off < int64(len(in.data)) && !in.pendingAt(off) {
		in.data[off] ^= mask
	}
	in.durable[off] ^= mask
	d := Damage{At: Duration(at), Kind: kind, Offset: off, Size: 1}
	in.damage = append(in.damage, d)
	return d
}

func (in *inode) pendingAt(off int64) bool {
	return slices.ContainsFunc(in.pending, func(w pendingWrite) bool {
		return w.off <= off && (w.truncate || off < w.off+int64(len(w.data)))
	})
}

func (m *memFS) rot(r *rand.Rand) (string, Damage, bool) {
	paths := sortedKeys(m.files)
	var total int64
	for _, p := range paths {
		total += int64(len(m.files[p].durable))
	}
	if total == 0 {
		return "", Damage{}, false
	}
	i := r.Int64N(total)
	for _, p := range paths {
		in := m.files[p]
		if i < int64(len(in.durable)) {
			return p, in.flip(i, byte(1)<<r.IntN(8), FaultBitrot, m.crashTime()), true
		}
		i -= int64(len(in.durable))
	}
	return "", Damage{}, false
}

type pageChunk struct {
	write int
	w     pendingWrite
}

func pageSpans(w pendingWrite) []pendingWrite {
	var out []pendingWrite
	end := w.off + int64(len(w.data))
	for off := w.off; off < end; {
		next := min((off/pageSize+1)*pageSize, end)
		out = append(out, pendingWrite{off: off, data: w.data[off-w.off : next-w.off]})
		off = next
	}
	return out
}

func pageChunks(pending []pendingWrite) map[int64][]pageChunk {
	pages := map[int64][]pageChunk{}
	for i, w := range pending {
		if w.truncate {
			pages[-1] = append(pages[-1], pageChunk{write: i, w: w})
			continue
		}
		for _, s := range pageSpans(w) {
			pages[s.off/pageSize] = append(pages[s.off/pageSize], pageChunk{write: i, w: s})
		}
	}
	return pages
}

func orderedChunks(pending []pendingWrite, size int64) ([]pageChunk, map[int64][]pageChunk) {
	var journal []pageChunk
	pages := map[int64][]pageChunk{}
	for i, w := range pending {
		if w.truncate {
			journal = append(journal, pageChunk{write: i, w: w})
			size = w.off
			continue
		}
		end := w.off + int64(len(w.data))
		split := min(max(size, w.off), end)
		for _, s := range pageSpans(pendingWrite{off: w.off, data: w.data[:split-w.off]}) {
			pages[s.off/pageSize] = append(pages[s.off/pageSize], pageChunk{write: i, w: pendingWrite{off: s.off, data: s.data, clip: true}})
		}
		for _, s := range pageSpans(pendingWrite{off: split, data: w.data[split-w.off:]}) {
			journal = append(journal, pageChunk{write: i, w: s})
		}
		size = max(size, end)
	}
	return journal, pages
}

func (in *inode) keepPrefix(c crashModel, at time.Duration, chunks []pageChunk) []pageChunk {
	n := c.pages.IntN(len(chunks) + 1)
	if c.torn && n > 0 && !chunks[n-1].w.truncate {
		chunks[n-1].w = in.tearWrite(c.pages, chunks[n-1].w, at)
	}
	return chunks[:n]
}

func (in *inode) survivingChunks(c crashModel, at time.Duration, size int64) []pageChunk {
	var kept []pageChunk
	pages := map[int64][]pageChunk{}
	if c.model == FSModelExt4 {
		var journal []pageChunk
		journal, pages = orderedChunks(in.pending, size)
		kept = in.keepPrefix(c, at, journal)
	} else {
		pages = pageChunks(in.pending)
	}
	for _, p := range slices.Sorted(maps.Keys(pages)) {
		kept = append(kept, in.keepPrefix(c, at, pages[p])...)
	}
	return kept
}

func (in *inode) writeBack(c crashModel, at time.Duration, data []byte) []byte {
	kept := in.survivingChunks(c, at, int64(len(data)))
	slices.SortStableFunc(kept, func(a, b pageChunk) int { return a.write - b.write })
	wb := &Writeback{At: Duration(at)}
	for _, w := range in.pending {
		wb.Writes = append(wb.Writes, WriteFate{Offset: w.off, Size: int64(len(w.data)), Truncate: w.truncate})
	}
	for _, c := range kept {
		c.w = c.w.within(int64(len(data)))
		data = c.w.apply(data)
		f := &wb.Writes[c.write]
		f.Kept += int64(len(c.w.data))
		f.Survived = f.Survived || c.w.truncate || f.Kept > 0
	}
	in.wb = wb
	in.synced = in.synced || len(kept) > 0
	return data
}

func (in *inode) tearWrite(r *rand.Rand, w pendingWrite, at time.Duration) pendingWrite {
	kept := r.IntN(len(w.data) + 1)
	if kept < len(w.data) {
		in.tear = &Tear{At: Duration(at), Offset: w.off, Kept: int64(kept), Size: int64(len(w.data))}
	}
	w.data = w.data[:kept]
	return w
}

func (w pendingWrite) within(size int64) pendingWrite {
	if w.clip {
		w.data = w.data[:max(min(int64(len(w.data)), size-w.off), 0)]
	}
	return w
}

func (w pendingWrite) apply(data []byte) []byte {
	if w.truncate {
		return resize(data, w.off)
	}
	if len(w.data) == 0 {
		return data
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
	st := sys.Stat_t{Ino: in.ino, Mode: 0o644, Nlink: in.nlink, Size: int64(len(in.data)), Mtim: in.mtime, Ctim: in.mtime, Atim: in.atime}
	if in.target != "" {
		st.Mode, st.Size = fs.ModeSymlink|0o777, int64(len(in.target))
	}
	return st
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
	f.fs.call("fd_read", f.path, off)
	if f.in == nil {
		return 0, fsapi.EISDIR
	}
	if f.in.atime <= f.in.mtime {
		f.in.atime = f.fs.clock()
	}
	n := 0
	if off < int64(len(f.in.data)) {
		n = copy(buf, f.in.data[off:])
	}
	if f.fs.readFault == nil {
		return n, 0
	}
	errno, i, mask := f.fs.readFault(f.path, off, n)
	if errno != 0 {
		return 0, errno
	}
	if mask != 0 {
		buf[i] ^= mask
		f.in.damage = append(f.in.damage, Damage{At: Duration(f.fs.crashTime()), Kind: FaultReadCorrupt, Offset: off + int64(i), Size: 1})
	}
	return n, 0
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
	f.fs.call("fd_readdir", f.path, int64(n))
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
		st, _ := f.fs.stat("path_stat", p, false)
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
	f.fs.call("fd_write", f.path, int64(len(buf)))
	if f.in == nil {
		return 0, fsapi.EISDIR
	}
	if f.fs.writeFails != nil {
		if errno := f.fs.writeFails(f.path); errno != 0 {
			f.fs.notify()
			return 0, errno
		}
	}
	n := f.fs.room(f.in, off, len(buf))
	if n < len(buf) && f.fs.diskFull != nil {
		f.fs.diskFull(f.path, n, len(buf))
	}
	if n == 0 && len(buf) > 0 {
		f.fs.notify()
		return 0, f.fs.noSpaceLeft()
	}
	if errno := f.record(pendingWrite{off: off, data: append([]byte(nil), buf[:n]...)}); errno != 0 {
		return 0, errno
	}
	return n, 0
}

func (m *memFS) room(in *inode, off int64, n int) int {
	if m.capacity <= 0 {
		return n
	}
	limit := int64(len(in.data)) + max(m.capacity-m.used(), 0)
	return int(min(int64(n), max(limit-off, 0)))
}

func (m *memFS) noSpaceLeft() fsapi.Errno {
	m.noSpace = true
	return fsapi.EIO
}

func (m *memFS) used() int64 {
	seen := map[*inode]bool{}
	var total int64
	for _, in := range m.files {
		if !seen[in] {
			seen[in] = true
			total += int64(len(in.data))
		}
	}
	return total
}

func (f *memFile) Truncate(size int64) fsapi.Errno {
	f.fs.call("fd_truncate", f.path, size)
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
	f.fs.call("fd_sync", f.path, 0)
	f.fs.notify()
	if f.fs.syncWait != nil {
		f.fs.syncWait()
	}
	if f.in == nil {
		if f.fs.ns != nil && !f.weakenedTo(DirsyncNoop) {
			f.fs.ns.commit(f.path)
		}
		f.fs.unsafeRenames = nil
		return 0
	}
	if f.fs.syncFails != nil && f.fs.syncFails() {
		f.in.pending = nil
		return fsapi.EIO
	}
	f.fs.unsafeRenames = nil
	if f.weakenedTo(FsyncNoop) {
		return 0
	}
	f.persist()
	return 0
}

func (f *memFile) persist() {
	in := f.in
	pending := f.durablePending()
	size := syncedSize(int64(len(in.durable)), pending)
	dirty := dirtyPages(pending, size)
	d, damaged := f.damage(dirty, size)
	var old []byte
	if damaged {
		old = pageBytes(in.durable, d.Offset)
	}
	for _, w := range pending {
		in.durable = w.apply(in.durable)
	}
	in.rewrite(dirty)
	if w, ok := lastWrite(pending, int64(len(in.durable))); ok && f.weakenedTo(FsyncCorrupts) {
		in.durable[w.off] ^= 1
	}
	if damaged {
		in.misplace(d, old)
	}
	in.pending = nil
	in.synced = true
	in.saved = [2]int64{in.atime, in.mtime}
}

func (f *memFile) damage(dirty []int64, size int64) (Damage, bool) {
	if f.fs.syncDamage == nil || len(dirty) == 0 {
		return Damage{}, false
	}
	d, ok := f.fs.syncDamage(f.path, dirty, (size+pageSize-1)/pageSize)
	d.At, d.Size = Duration(f.fs.crashTime()), min(pageSize, size-d.Offset)
	return d, ok
}

func syncedSize(size int64, pending []pendingWrite) int64 {
	for _, w := range pending {
		if w.truncate {
			size = w.off
			continue
		}
		size = max(size, w.off+int64(len(w.data)))
	}
	return size
}

func dirtyPages(pending []pendingWrite, size int64) []int64 {
	var pages []int64
	for _, w := range pending {
		if w.truncate {
			continue
		}
		for p := w.off / pageSize; p*pageSize < min(w.off+int64(len(w.data)), size); p++ {
			pages = append(pages, p)
		}
	}
	slices.Sort(pages)
	return slices.Compact(pages)
}

func lastWrite(pending []pendingWrite, size int64) (pendingWrite, bool) {
	for _, w := range slices.Backward(pending) {
		if !w.truncate && len(w.data) > 0 && w.off < size {
			return w, true
		}
	}
	return pendingWrite{}, false
}

func pageBytes(data []byte, off int64) []byte {
	page := make([]byte, pageSize)
	if off < int64(len(data)) {
		copy(page, data[off:])
	}
	return page
}

func (in *inode) misplace(d Damage, old []byte) {
	moved := pageBytes(in.durable, d.Offset)
	copy(in.durable[d.Offset:d.Offset+d.Size], old)
	if in.stale == nil {
		in.stale = map[int64]bool{}
	}
	in.stale[d.Offset/pageSize] = true
	if d.Kind == FaultMisdirected {
		copy(in.durable[d.Target:min(d.Target+pageSize, int64(len(in.durable)))], moved)
		in.stale[d.Target/pageSize] = true
	}
	in.damage = append(in.damage, d)
}

func (in *inode) rewrite(dirty []int64) {
	for _, p := range dirty {
		if !in.stale[p] {
			continue
		}
		start, end := p*pageSize, min((p+1)*pageSize, int64(len(in.durable)), int64(len(in.data)))
		if start < end {
			copy(in.durable[start:end], in.data[start:end])
		}
		delete(in.stale, p)
	}
}

func (f *memFile) Datasync() fsapi.Errno { return f.Sync() }
func (f *memFile) Close() fsapi.Errno    { return 0 }

func (f *memFile) Utimens(atim, mtim int64) fsapi.Errno {
	if f.in != nil {
		f.fs.call("fd_utimens", f.path, mtim)
		f.in.setTimes(atim, mtim)
	}
	return 0
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
