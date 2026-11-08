package engine

import (
	"bytes"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
)

const rw = fsapi.O_CREAT | fsapi.O_RDWR

func testFS() *memFS { return newMemFS(func() int64 { return epochNanos }) }

func open(t *testing.T, m *memFS, p string, flag fsapi.Oflag) *memFile {
	t.Helper()
	f, errno := m.OpenFile(p, flag, 0)
	if errno != 0 {
		t.Fatalf("open %s: %v", p, errno)
	}
	return f.(*memFile)
}

func write(t *testing.T, f *memFile, s string) {
	t.Helper()
	if _, errno := f.Write([]byte(s)); errno != 0 {
		t.Fatalf("write %q: %v", s, errno)
	}
}

func fsync(t *testing.T, f *memFile) {
	t.Helper()
	if errno := f.Sync(); errno != 0 {
		t.Fatalf("sync %s: %v", f.path, errno)
	}
}

func contents(m *memFS, p string) (string, bool) {
	in, ok := m.files[p]
	if !ok {
		return "", false
	}
	return string(in.data), true
}

func seeded(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed)) }

func TestCrashKeepsOnlySyncedData(t *testing.T) {
	tests := []struct {
		name  string
		steps func(t *testing.T, m *memFS)
		want  string
		alive bool
	}{
		{"synced write survives", func(t *testing.T, m *memFS) {
			f := open(t, m, "log", rw)
			write(t, f, "abc")
			fsync(t, f)
		}, "abc", true},
		{"writes after the last sync are lost", func(t *testing.T, m *memFS) {
			f := open(t, m, "log", rw)
			write(t, f, "abc")
			fsync(t, f)
			write(t, f, "def")
		}, "abc", true},
		{"a file never synced disappears", func(t *testing.T, m *memFS) {
			write(t, open(t, m, "log", rw), "abc")
		}, "", false},
		{"O_SYNC makes every write durable", func(t *testing.T, m *memFS) {
			write(t, open(t, m, "log", rw|fsapi.O_SYNC), "abc")
		}, "abc", true},
		{"an unsynced truncate is undone", func(t *testing.T, m *memFS) {
			f := open(t, m, "log", rw)
			write(t, f, "abcdef")
			fsync(t, f)
			f.Truncate(2)
		}, "abcdef", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testFS()
			tt.steps(t, m)
			m.crash(seeded(1), crashModel{})
			got, alive := contents(m, "log")
			if got != tt.want || alive != tt.alive {
				t.Fatalf("after crash: %q (exists %v), want %q (exists %v)", got, alive, tt.want, tt.alive)
			}
		})
	}
}

func TestTornCrashPersistsAPrefixOfPendingWrites(t *testing.T) {
	writes := []string{"aaaa", "bbbb", "cccc"}
	full := "base" + strings.Join(writes, "")
	outcomes := map[int]bool{}
	for seed := range uint64(300) {
		m := testFS()
		f := open(t, m, "log", rw)
		write(t, f, "base")
		fsync(t, f)
		for _, w := range writes {
			write(t, f, w)
		}
		m.crash(seeded(seed), crashModel{torn: true})
		got, _ := contents(m, "log")
		if !strings.HasPrefix(full, got) || len(got) < len("base") {
			t.Fatalf("seed %d: recovered %q is not a prefix of %q at least as long as the synced data", seed, got, full)
		}
		if (len(got)-len("base"))%len(writes[0]) != 0 && m.files["log"].tear == nil {
			t.Fatalf("seed %d: recovered %q mid-write but recorded no tear", seed, got)
		}
		outcomes[len(got)] = true
	}
	for _, n := range []int{len("base"), len(full)} {
		if !outcomes[n] {
			t.Errorf("no seed recovered %d bytes; outcomes %v", n, outcomes)
		}
	}
	if len(outcomes) <= len(writes)+1 {
		t.Errorf("no seed tore a write mid-way; outcomes %v", outcomes)
	}
}

func TestTearWriteKeepsAPrefix(t *testing.T) {
	r := seeded(7)
	in := &inode{}
	w := pendingWrite{off: 10, data: []byte("0123456789")}
	for range 100 {
		got := in.tearWrite(r, w, 0)
		if got.off != w.off || !bytes.HasPrefix(w.data, got.data) {
			t.Fatalf("tearWrite(%q) = %q at %d, want a prefix at %d", w.data, got.data, got.off, w.off)
		}
		if len(got.data) < len(w.data) && (in.tear == nil || in.tear.Kept != int64(len(got.data))) {
			t.Fatalf("tear of %d bytes not recorded: %+v", len(got.data), in.tear)
		}
	}
}

func TestPageChunksSplitWritesAtPageBoundaries(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 2*pageSize)
	pages := pageChunks([]pendingWrite{
		{off: pageSize - 10, data: data},
		{off: 5, truncate: true},
		{off: 0, data: []byte("head")},
	})
	want := map[int64][]pageChunk{
		-1: {{1, pendingWrite{off: 5, truncate: true}}},
		0:  {{0, pendingWrite{off: pageSize - 10, data: data[:10]}}, {2, pendingWrite{off: 0, data: []byte("head")}}},
		1:  {{0, pendingWrite{off: pageSize, data: data[10 : 10+pageSize]}}},
		2:  {{0, pendingWrite{off: 2 * pageSize, data: data[10+pageSize:]}}},
	}
	if len(pages) != len(want) {
		t.Fatalf("got pages %v, want %v", slices.Sorted(maps.Keys(pages)), slices.Sorted(maps.Keys(want)))
	}
	for p, chunks := range want {
		got := pages[p]
		if len(got) != len(chunks) {
			t.Fatalf("page %d: %d chunks, want %d", p, len(got), len(chunks))
		}
		for i, c := range chunks {
			g := got[i]
			if g.write != c.write || g.w.off != c.w.off || g.w.truncate != c.w.truncate || !bytes.Equal(g.w.data, c.w.data) {
				t.Fatalf("page %d chunk %d: write %d off %d len %d, want write %d off %d len %d", p, i, g.write, g.w.off, len(g.w.data), c.write, c.w.off, len(c.w.data))
			}
		}
	}
}

func TestWritebackPersistsPagesIndependently(t *testing.T) {
	page := func(c byte) string { return strings.Repeat(string(c), pageSize) }
	seen := map[string]bool{}
	for seed := range uint64(200) {
		m := testFS()
		f := open(t, m, "db", rw)
		write(t, f, page('a'))
		write(t, f, page('b'))
		m.crash(seeded(seed), crashModel{pages: seeded(seed + 1000)})
		got, alive := contents(m, "db")
		key := "none"
		switch {
		case !alive:
		case got == page('a')+page('b'):
			key = "both"
		case got == page('a'):
			key = "first"
		case got == strings.Repeat("\x00", pageSize)+page('b'):
			key = "second"
		default:
			t.Fatalf("seed %d: unexpected image of %d bytes", seed, len(got))
		}
		seen[key] = true
		if alive {
			fates := m.files["db"].wb.Writes
			if len(fates) != 2 || fates[0].Survived != (key != "second") || fates[1].Survived != (key != "first") {
				t.Fatalf("seed %d: image %s but writeback fates %+v", seed, key, fates)
			}
		}
	}
	for _, k := range []string{"none", "first", "second", "both"} {
		if !seen[k] {
			t.Errorf("no seed produced outcome %q; got %v", k, seen)
		}
	}
}

func TestWritebackKeepsOrderWithinAPage(t *testing.T) {
	seen := map[string]bool{}
	for seed := range uint64(200) {
		m := testFS()
		f := open(t, m, "db", rw)
		write(t, f, "aaaa")
		f.Pwrite([]byte("bb"), 0)
		m.crash(seeded(seed), crashModel{pages: seeded(seed + 1000)})
		got, _ := contents(m, "db")
		if got != "" && got != "aaaa" && got != "bbaa" {
			t.Fatalf("seed %d: page image %q, want a prefix of the writes to it", seed, got)
		}
		seen[got] = true
	}
	if len(seen) != 3 {
		t.Errorf("outcomes %v, want all three prefixes", seen)
	}
}

func TestFsyncEIODropsPendingWritesButLeavesThemReadable(t *testing.T) {
	m := testFS()
	fail := false
	m.syncFails = func() bool { return fail }
	f := open(t, m, "log", rw)
	write(t, f, "ok")
	fsync(t, f)
	write(t, f, "lost")
	fail = true
	if errno := f.Sync(); errno != fsapi.EIO {
		t.Fatalf("sync returned %v, want EIO", errno)
	}
	fail = false
	if got, _ := contents(m, "log"); got != "oklost" {
		t.Fatalf("after EIO the page cache holds %q, want %q", got, "oklost")
	}
	fsync(t, f)
	m.crash(seeded(1), crashModel{})
	if got, _ := contents(m, "log"); got != "ok" {
		t.Fatalf("a retried fsync after EIO persisted %q, want %q", got, "ok")
	}
}

func TestNamespaceOperations(t *testing.T) {
	m := testFS()
	f := open(t, m, "a", rw)
	write(t, f, "data")
	errnos := []struct {
		name string
		got  fsapi.Errno
		want fsapi.Errno
	}{
		{"exclusive create of an existing file", openErrno(m, "a", rw|fsapi.O_EXCL), fsapi.EEXIST},
		{"open without O_CREAT", openErrno(m, "missing", fsapi.O_RDWR), fsapi.ENOENT},
		{"create in a missing directory", openErrno(m, "nodir/f", rw), fsapi.ENOENT},
		{"open a file as a directory", openErrno(m, "a", fsapi.O_DIRECTORY), fsapi.ENOTDIR},
		{"open a directory for writing", openErrno(m, ".", fsapi.O_RDWR), fsapi.EISDIR},
		{"mkdir", m.Mkdir("d", 0), 0},
		{"mkdir an existing name", m.Mkdir("a", 0), fsapi.EEXIST},
		{"rename into a directory", m.Rename("a", "d/b"), 0},
		{"rename a missing file", m.Rename("a", "c"), fsapi.ENOENT},
		{"rmdir a non-empty directory", m.Rmdir("d"), fsapi.ENOTEMPTY},
		{"unlink a directory", m.Unlink("d"), fsapi.EISDIR},
		{"rename a directory", m.Rename("d", "e"), 0},
		{"unlink", m.Unlink("e/b"), 0},
		{"unlink twice", m.Unlink("e/b"), fsapi.ENOENT},
		{"rmdir", m.Rmdir("e"), 0},
	}
	for _, e := range errnos {
		if e.got != e.want {
			t.Errorf("%s: got %v, want %v", e.name, e.got, e.want)
		}
	}
	if len(m.files) != 0 || len(m.dirs) != 1 {
		t.Fatalf("left files %v dirs %v, want only the root", sortedKeys(m.files), sortedKeys(m.dirs))
	}
}

func openErrno(m *memFS, p string, flag fsapi.Oflag) fsapi.Errno {
	_, errno := m.OpenFile(p, flag, 0)
	return errno
}

func TestRenameKeepsContentAndTruncateResets(t *testing.T) {
	m := testFS()
	write(t, open(t, m, "tmp", rw), "payload")
	if errno := m.Rename("tmp", "final"); errno != 0 {
		t.Fatal(errno)
	}
	if got, _ := contents(m, "final"); got != "payload" {
		t.Fatalf("renamed file holds %q", got)
	}
	open(t, m, "final", fsapi.O_RDWR|fsapi.O_TRUNC)
	if got, _ := contents(m, "final"); got != "" {
		t.Fatalf("O_TRUNC left %q", got)
	}
}

func TestReaddirListsChildrenSorted(t *testing.T) {
	m := testFS()
	for _, p := range []string{"c", "a", "b"} {
		open(t, m, p, rw)
	}
	m.Mkdir("dir", 0)
	open(t, m, "dir/nested", rw)
	entries, errno := open(t, m, ".", fsapi.O_RDONLY).Readdir(-1)
	if errno != 0 {
		t.Fatal(errno)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if want := []string{"a", "b", "c", "dir"}; !slices.Equal(names, want) {
		t.Fatalf("readdir = %v, want %v", names, want)
	}
}
