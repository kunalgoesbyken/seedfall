package engine

import (
	"io/fs"
	"testing"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
)

func TestSymlinksResolveAndLstatSeesTheLink(t *testing.T) {
	m := testFS()
	if errno := m.Mkdir("d", 0); errno != 0 {
		t.Fatal(errno)
	}
	write(t, open(t, m, "d/f", rw), "hello")
	for _, link := range []struct{ target, name string }{{"d/f", "abs"}, {"f", "d/rel"}, {"d", "dirlink"}} {
		if errno := m.Symlink(link.target, link.name); errno != 0 {
			t.Fatalf("symlink %s: %v", link.name, errno)
		}
	}
	for _, p := range []string{"abs", "d/rel", "dirlink/f"} {
		st, errno := m.Stat(p)
		if errno != 0 || st.Size != 5 || st.Mode.Type() != 0 {
			t.Fatalf("stat %s = %+v, %v, want the 5-byte regular file", p, st, errno)
		}
	}
	st, errno := m.Lstat("abs")
	if errno != 0 || st.Mode.Type() != fs.ModeSymlink || st.Size != int64(len("d/f")) {
		t.Fatalf("lstat = %+v, %v, want a symlink with the target's length", st, errno)
	}
	if got, errno := m.Readlink("abs"); errno != 0 || got != "d/f" {
		t.Fatalf("readlink = %q, %v", got, errno)
	}
	if _, errno := m.Readlink("d/f"); errno != fsapi.EINVAL {
		t.Fatalf("readlink of a file = %v, want EINVAL", errno)
	}
	if errno := m.Unlink("abs"); errno != 0 {
		t.Fatal(errno)
	}
	if _, ok := contents(m, "d/f"); !ok {
		t.Fatal("unlinking a symlink removed its target")
	}
}

func TestSymlinkLoopsFailWithELOOP(t *testing.T) {
	m := testFS()
	for _, l := range [][2]string{{"b", "a"}, {"a", "b"}} {
		if errno := m.Symlink(l[0], l[1]); errno != 0 {
			t.Fatal(errno)
		}
	}
	if _, errno := m.Stat("a"); errno != fsapi.ELOOP {
		t.Fatalf("stat of a loop = %v, want ELOOP", errno)
	}
	if _, errno := m.OpenFile("a", fsapi.O_RDONLY, 0); errno != fsapi.ELOOP {
		t.Fatalf("open of a loop = %v, want ELOOP", errno)
	}
	if st, errno := m.Lstat("a"); errno != 0 || st.Mode.Type() != fs.ModeSymlink {
		t.Fatalf("lstat of a loop = %+v, %v", st, errno)
	}
}

func TestDanglingSymlinkCreatesItsTarget(t *testing.T) {
	m := testFS()
	if errno := m.Symlink("target", "link"); errno != 0 {
		t.Fatal(errno)
	}
	write(t, open(t, m, "link", rw), "x")
	if got, ok := contents(m, "target"); !ok || got != "x" {
		t.Fatalf("target = %q, %v", got, ok)
	}
	if _, errno := m.OpenFile("link", fsapi.O_RDONLY|fsapi.O_NOFOLLOW, 0); errno != fsapi.ELOOP {
		t.Fatalf("O_NOFOLLOW on a symlink = %v, want ELOOP", errno)
	}
}

func TestHardLinksShareAnInodeAndCountLinks(t *testing.T) {
	m := testFS()
	f := open(t, m, "a", rw)
	write(t, f, "data")
	if errno := m.Link("a", "b"); errno != 0 {
		t.Fatal(errno)
	}
	if errno := m.Link("a", "b"); errno != fsapi.EEXIST {
		t.Fatalf("second link = %v, want EEXIST", errno)
	}
	st, _ := m.Stat("b")
	if st.Nlink != 2 {
		t.Fatalf("nlink = %d, want 2", st.Nlink)
	}
	write(t, open(t, m, "b", fsapi.O_WRONLY|fsapi.O_APPEND), "!")
	if got, _ := contents(m, "a"); got != "data!" {
		t.Fatalf("a = %q, want the write through b", got)
	}
	if m.capacity = 1 << 20; m.used() != 5 {
		t.Fatalf("used = %d, want one copy of the inode counted", m.used())
	}
	if errno := m.Rename("a", "b"); errno != 0 {
		t.Fatal(errno)
	}
	if _, ok := contents(m, "a"); !ok {
		t.Fatal("renaming a file onto another link of itself must do nothing")
	}
	if errno := m.Unlink("a"); errno != 0 {
		t.Fatal(errno)
	}
	if st, _ := m.Stat("b"); st.Nlink != 1 {
		t.Fatalf("nlink after unlink = %d, want 1", st.Nlink)
	}
	if errno := m.Link(".", "x"); errno != fsapi.EPERM {
		t.Fatalf("hard link to a directory = %v, want EPERM", errno)
	}
}

func TestStrictHardLinkNeedsTheDirectoryFsync(t *testing.T) {
	m := strictFS()
	f := open(t, m, "a", rw)
	write(t, f, "data")
	fsync(t, f)
	syncDir(t, m, ".")
	if errno := m.Link("a", "b"); errno != 0 {
		t.Fatal(errno)
	}
	m.crash(seeded(1), crashModel{})
	if _, ok := contents(m, "b"); ok {
		t.Fatal("a link whose directory was never fsynced survived")
	}
	if st, _ := m.Stat("a"); st.Nlink != 1 {
		t.Fatalf("nlink after recovery = %d, want 1", st.Nlink)
	}
	if errno := m.Link("a", "b"); errno != 0 {
		t.Fatal(errno)
	}
	syncDir(t, m, ".")
	m.crash(seeded(1), crashModel{})
	if st, _ := m.Stat("b"); st.Nlink != 2 {
		t.Fatalf("nlink of a durable link = %d, want 2", st.Nlink)
	}
}

func TestStrictSymlinkNeedsTheDirectoryFsync(t *testing.T) {
	m := strictFS()
	if errno := m.Symlink("x", "l"); errno != 0 {
		t.Fatal(errno)
	}
	m.crash(seeded(1), crashModel{})
	if _, errno := m.Lstat("l"); errno != fsapi.ENOENT {
		t.Fatalf("lstat = %v, want ENOENT", errno)
	}
}

func TestLooseCrashKeepsSymlinks(t *testing.T) {
	m := testFS()
	if errno := m.Symlink("x", "l"); errno != 0 {
		t.Fatal(errno)
	}
	m.crash(seeded(1), crashModel{})
	if got, errno := m.Readlink("l"); errno != 0 || got != "x" {
		t.Fatalf("readlink after crash = %q, %v", got, errno)
	}
}

func TestUtimensStoresTimesAndACrashRevertsUnsyncedOnes(t *testing.T) {
	now := int64(1000)
	m := newMemFS(func() int64 { return now })
	f := open(t, m, "a", rw)
	write(t, f, "x")
	fsync(t, f)
	if errno := m.Utimens("a", 7, fsapi.UTIME_OMIT); errno != 0 {
		t.Fatal(errno)
	}
	st, _ := m.Stat("a")
	if st.Atim != 7 || st.Mtim != 1000 {
		t.Fatalf("times = atim %d mtim %d, want 7 and 1000", st.Atim, st.Mtim)
	}
	if errno := m.Utimens("a", 7, 9); errno != 0 {
		t.Fatal(errno)
	}
	m.crash(seeded(1), crashModel{})
	st, _ = m.Stat("a")
	if st.Mtim != 1000 || st.Atim != 1000 {
		t.Fatalf("times after crash = atim %d mtim %d, want the fsynced 1000 and 1000", st.Atim, st.Mtim)
	}
}

func TestRelatimeUpdatesAccessTimeOnlyWhenStale(t *testing.T) {
	now := int64(1000)
	m := newMemFS(func() int64 { return now })
	f := open(t, m, "a", rw)
	write(t, f, "x")
	now = 2000
	if _, errno := f.Pread(make([]byte, 1), 0); errno != 0 {
		t.Fatal(errno)
	}
	now = 3000
	if _, errno := f.Pread(make([]byte, 1), 0); errno != 0 {
		t.Fatal(errno)
	}
	if st, _ := m.Stat("a"); st.Atim != 2000 {
		t.Fatalf("atime = %d, want the first read's 2000", st.Atim)
	}
}

func TestExt4OrderedKeepsAppendsInOrderAndOverwritesFree(t *testing.T) {
	sawReorderedOverwrite := false
	for seed := uint64(0); seed < 400; seed++ {
		m := testFS()
		f := open(t, m, "log", rw)
		write(t, f, string(make([]byte, 2*pageSize)))
		fsync(t, f)
		if _, errno := f.Pwrite([]byte("O"), 0); errno != 0 {
			t.Fatal(errno)
		}
		for i := int64(0); i < 3; i++ {
			if _, errno := f.Pwrite([]byte{'A' + byte(i)}, 2*pageSize+i*pageSize); errno != 0 {
				t.Fatal(errno)
			}
		}
		m.crash(seeded(seed), crashModel{pages: seeded(seed + 1000), model: FSModelExt4})
		got := []byte(m.files["log"].data)
		size := int64(len(got))
		switch {
		case size == 2*pageSize:
		case size == 2*pageSize+1 && got[2*pageSize] == 'A':
		case size == 3*pageSize+1 && got[2*pageSize] == 'A' && got[3*pageSize] == 'B':
		case size == 4*pageSize+1 && got[2*pageSize] == 'A' && got[3*pageSize] == 'B' && got[4*pageSize] == 'C':
		default:
			t.Fatalf("seed %d: size %d is not an in-order prefix of the appends", seed, size)
		}
		if got[0] == 'O' && size == 2*pageSize {
			sawReorderedOverwrite = true
		}
	}
	if !sawReorderedOverwrite {
		t.Fatal("no seed persisted the overwrite without the appends written after it")
	}
}

func TestExt4OrderedNeverExtendsTheFileThroughAnOverwrite(t *testing.T) {
	for seed := uint64(0); seed < 100; seed++ {
		m := testFS()
		f := open(t, m, "log", rw)
		write(t, f, "aaaa")
		fsync(t, f)
		write(t, f, "bbbb")
		if _, errno := f.Pwrite([]byte("CC"), 6); errno != 0 {
			t.Fatal(errno)
		}
		m.crash(seeded(seed), crashModel{pages: seeded(seed + 1000), model: FSModelExt4})
		if got := string(m.files["log"].data); got != "aaaa" && got != "aaaabbbb" && got != "aaaabbCC" {
			t.Fatalf("seed %d: %q is not a state ext4 could produce", seed, got)
		}
	}
}

func TestPageIndependentCanExtendTheFileOutOfOrder(t *testing.T) {
	for seed := uint64(0); seed < 400; seed++ {
		m := testFS()
		f := open(t, m, "log", rw)
		if _, errno := f.Pwrite([]byte("A"), 0); errno != 0 {
			t.Fatal(errno)
		}
		if _, errno := f.Pwrite([]byte("B"), 3*pageSize); errno != 0 {
			t.Fatal(errno)
		}
		fsync(t, open(t, m, "other", rw))
		m.crash(seeded(seed), crashModel{pages: seeded(seed + 1000), model: FSModelPages})
		if in, ok := m.files["log"]; ok && len(in.data) > 0 && in.data[0] == 0 {
			return
		}
	}
	t.Fatal("no seed kept the later page without the earlier one")
}

func TestDirReorderRecoversASubsetOfNamespaceOps(t *testing.T) {
	survivors := map[string]bool{}
	for seed := uint64(0); seed < 100; seed++ {
		m := strictFS()
		for _, p := range []string{"a", "b", "c"} {
			fsync(t, open(t, m, p, rw))
		}
		m.crash(seeded(seed), crashModel{pages: seeded(seed + 1000), model: FSModelDirs})
		state := ""
		for _, p := range []string{"a", "b", "c"} {
			if _, ok := contents(m, p); ok {
				state += p
			}
		}
		survivors[state] = true
	}
	if !survivors["c"] && !survivors["ac"] && !survivors["bc"] {
		t.Fatalf("a later entry never survived without an earlier one: %v", survivors)
	}
}

func TestStrictPrefixModelKeepsNamespaceOrder(t *testing.T) {
	for seed := uint64(0); seed < 100; seed++ {
		m := strictFS()
		for _, p := range []string{"a", "b"} {
			fsync(t, open(t, m, p, rw))
		}
		m.crash(seeded(seed), crashModel{torn: true, pages: seeded(seed + 1000), model: FSModelExt4})
		if _, b := contents(m, "b"); b {
			if _, a := contents(m, "a"); !a {
				t.Fatalf("seed %d: b survived without a under the journaled model", seed)
			}
		}
	}
}

func TestFSModelValidation(t *testing.T) {
	cases := []struct {
		name string
		spec FaultSpec
		ok   bool
	}{
		{"default", FaultSpec{}, true},
		{"unknown", FaultSpec{ReorderWrites: true, FSModel: "ext2"}, false},
		{"needs reorder", FaultSpec{FSModel: FSModelExt4}, false},
		{"pages", FaultSpec{ReorderWrites: true, FSModel: FSModelPages}, true},
		{"dir reorder needs strict", FaultSpec{ReorderWrites: true, FSModel: FSModelDirs}, false},
		{"dir reorder", FaultSpec{ReorderWrites: true, StrictDirs: true, FSModel: FSModelDirs}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.spec.checkFSModel(); (err == nil) != c.ok {
				t.Fatalf("checkFSModel() = %v, want ok=%v", err, c.ok)
			}
		})
	}
}
