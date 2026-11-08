package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"
)

type fakeMemory struct {
	api.Memory
	b []byte
}

func (m *fakeMemory) Read(off, n uint32) ([]byte, bool) {
	if uint64(off)+uint64(n) > uint64(len(m.b)) {
		return nil, false
	}
	return m.b[off : off+n], true
}

func mapFile(t *testing.T, m *memFS, mem *fakeMemory, name string, flags int32) (*memFile, uint32) {
	t.Helper()
	f := open(t, m, name, rw)
	if len(f.in.data) == 0 {
		write(t, f, strings.Repeat("a", 2*pageSize))
		fsync(t, f)
	}
	if code := m.mmap(mem, f.in.ino, 0, uint32(len(mem.b)), 0, flags); code != mmapOK {
		t.Fatalf("mmap = %d", code)
	}
	return f, 0
}

func TestMmapCopiesTheFileAndSeesLaterWrites(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, 2*pageSize)}
	f, _ := mapFile(t, m, mem, "f", mmapShared)
	if mem.b[0] != 'a' {
		t.Fatalf("the mapping does not hold the file: %q", mem.b[:4])
	}
	if _, errno := f.Pwrite([]byte("zz"), pageSize); errno != 0 {
		t.Fatal(errno)
	}
	if string(mem.b[pageSize:pageSize+2]) != "zz" {
		t.Fatalf("pwrite did not reach the shared mapping: %q", mem.b[pageSize:pageSize+2])
	}
}

func TestMsyncPersistsMappedPagesAndADirtyPageIsLostOnCrash(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, 2*pageSize)}
	f, ptr := mapFile(t, m, mem, "f", mmapShared|mmapWritable)
	mem.b[0] = 'S'
	if code := m.msync(ptr, pageSize, msyncSync); code != mmapOK {
		t.Fatalf("msync = %d", code)
	}
	mem.b[pageSize] = 'D'
	m.endInstance(true)
	m.crash(seeded(1), crashModel{})
	if got := f.in.data[0]; got != 'S' {
		t.Fatalf("the msynced page reads %q after a crash", got)
	}
	if got := f.in.data[pageSize]; got != 'a' {
		t.Fatalf("the unsynced page reads %q after a crash, want it lost", got)
	}
}

func TestEarlyWritebackKeepsChosenDirtyPages(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, 2*pageSize)}
	f, _ := mapFile(t, m, mem, "f", mmapShared|mmapWritable)
	mem.b[0] = 'X'
	mem.b[pageSize] = 'Y'
	var seen []int64
	m.endInstance(true)
	m.crash(seeded(1), crashModel{mapped: func(_ string, off int64) bool {
		seen = append(seen, off)
		return off == pageSize
	}})
	if len(seen) != 2 || seen[0] != 0 || seen[1] != pageSize {
		t.Fatalf("dirty pages offered = %v, want both in order", seen)
	}
	if f.in.data[0] != 'a' || f.in.data[pageSize] != 'Y' {
		t.Fatalf("after crash pages are %q and %q, want a and Y", f.in.data[0], f.in.data[pageSize])
	}
}

func TestReadSeesDirtyMappedPages(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, 2*pageSize)}
	f, _ := mapFile(t, m, mem, "f", mmapShared|mmapWritable)
	mem.b[10] = 'Q'
	buf := make([]byte, 1)
	if _, errno := f.Pread(buf, 10); errno != 0 {
		t.Fatal(errno)
	}
	if buf[0] != 'Q' {
		t.Fatalf("read returned %q, want the mapped write", buf[0])
	}
	if len(f.in.pending) == 0 {
		t.Fatal("the mapped write should be a pending write once flushed")
	}
}

func TestPrivateMappingsAreSnapshots(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, pageSize)}
	f, _ := mapFile(t, m, mem, "f", 0)
	if len(m.maps) != 0 {
		t.Fatal("a private mapping was registered")
	}
	if _, errno := f.Pwrite([]byte("z"), 0); errno != 0 {
		t.Fatal(errno)
	}
	if mem.b[0] != 'a' {
		t.Fatal("a private mapping changed after a pwrite")
	}
}

func TestCleanExitFlushesDirtyPagesToTheCache(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, pageSize)}
	f, _ := mapFile(t, m, mem, "f", mmapShared|mmapWritable)
	mem.b[0] = 'W'
	m.endInstance(false)
	if f.in.data[0] != 'W' || len(m.crashDirty) != 0 {
		t.Fatalf("data %q, crash dirty %d", f.in.data[0], len(m.crashDirty))
	}
	if len(f.in.pending) == 0 {
		t.Fatal("a clean exit should leave the page pending, not durable")
	}
}

func packageDir(t *testing.T, files map[string]string) goPackage {
	t.Helper()
	dir := t.TempDir()
	var goFiles, ignored []string
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(content, "!linux") {
			goFiles = append(goFiles, name)
		} else {
			ignored = append(ignored, name)
		}
	}
	return goPackage{ImportPath: "example.com/pkg", Dir: dir, GoFiles: goFiles, Ignored: ignored, Module: &goModule{Path: "example.com/pkg"}}
}

func TestWidenTakesOverAFallbackFile(t *testing.T) {
	p := packageDir(t, map[string]string{
		"lock_unix.go":  "//go:build linux || darwin\n\npackage p\n\nimport \"golang.org/x/sys/unix\"\n\nfunc Lock(fd int) error { return unix.Flock(fd, unix.LOCK_EX) }\n",
		"lock_other.go": "//go:build !linux\n\npackage p\n\nfunc Lock(fd int) error { return nil }\n",
	})
	widened, err := widen(p, nil)
	if err != nil || len(widened) != 1 {
		t.Fatalf("widened %d files, err %v", len(widened), err)
	}
	f := widened[0]
	if f.name != "lock_unix.go" || !f.unix || len(f.removes) != 1 || f.removes[0] != "lock_other.go" {
		t.Fatalf("widened = %+v", f)
	}
	if !strings.HasPrefix(string(f.source), "//go:build wasip1\n\n") || strings.Contains(string(f.source), "darwin") {
		t.Fatalf("source constraint not rewritten:\n%s", f.source)
	}
}

func TestWidenSkipsAFileAFallbackHasMoreThan(t *testing.T) {
	p := packageDir(t, map[string]string{
		"lock_unix.go":  "//go:build linux\n\npackage p\n\nimport \"syscall\"\n\nfunc Lock(fd int) error { return syscall.Flock(fd, 2) }\n",
		"lock_other.go": "//go:build !linux\n\npackage p\n\nfunc Lock(fd int) error { return nil }\nfunc Extra() {}\n",
	})
	widened, err := widen(p, nil)
	if err != nil || len(widened) != 0 {
		t.Fatalf("widened %d files, err %v; a fallback with extra declarations must stay", len(widened), err)
	}
}

func TestWidenSkipsFilesThatNeverMap(t *testing.T) {
	p := packageDir(t, map[string]string{
		"sock_unix.go": "//go:build linux\n\npackage p\n\nimport \"syscall\"\n\nfunc F() error { return syscall.EINVAL }\n",
	})
	if widened, _ := widen(p, nil); len(widened) != 0 {
		t.Fatalf("widened %v", widened)
	}
}

func TestWidenSkipsLinuxOnlyFileNames(t *testing.T) {
	p := packageDir(t, map[string]string{
		"x_linux.go": "package p\n\nimport \"syscall\"\n\nfunc F() error { return syscall.Flock(0, 2) }\n",
	})
	if widened, _ := widen(p, nil); len(widened) != 0 {
		t.Fatalf("a _linux.go file was widened: %v", widened)
	}
}

func TestCompatCoverageNamesTheShim(t *testing.T) {
	got, err := CompatCoverage(CompatDep{ImportPath: "go.etcd.io/bbolt", Module: "go.etcd.io/bbolt"})
	if err != nil || !strings.Contains(got, "go.etcd.io/bbolt") {
		t.Fatalf("coverage = %q, %v", got, err)
	}
	if got, err := CompatCoverage(CompatDep{ImportPath: "example.com/none", Module: "example.com/none", Dir: t.TempDir()}); err != nil || got != "" {
		t.Fatalf("coverage = %q, %v, want none", got, err)
	}
}

func TestFsyncPersistsDirtyMappedPages(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, pageSize)}
	f, _ := mapFile(t, m, mem, "f", mmapShared|mmapWritable)
	mem.b[0] = 'F'
	fsync(t, f)
	m.endInstance(true)
	m.crash(seeded(1), crashModel{})
	if got := f.in.data[0]; got != 'F' {
		t.Fatalf("a mapped write reads %q after fsync and a crash, want it durable", got)
	}
}

func TestRangedMsyncLeavesOtherMappedPagesDirty(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, 2*pageSize)}
	f, ptr := mapFile(t, m, mem, "f", mmapShared|mmapWritable)
	mem.b[0], mem.b[pageSize] = 'S', 'D'
	if code := m.msync(ptr, pageSize, msyncSync); code != mmapOK {
		t.Fatalf("msync = %d", code)
	}
	m.endInstance(true)
	m.crash(seeded(1), crashModel{})
	if f.in.data[0] != 'S' || f.in.data[pageSize] != 'a' {
		t.Fatalf("after a crash pages are %q and %q, want S and a", f.in.data[0], f.in.data[pageSize])
	}
}

func TestNewMappingSeesAnotherMappingsDirtyPages(t *testing.T) {
	m := testFS()
	first := &fakeMemory{b: make([]byte, pageSize)}
	f, _ := mapFile(t, m, first, "f", mmapShared|mmapWritable)
	first.b[0] = 'N'
	second := &fakeMemory{b: make([]byte, pageSize)}
	if code := m.mmap(second, f.in.ino, 0, pageSize, 0, mmapShared); code != mmapOK {
		t.Fatalf("mmap = %d", code)
	}
	if second.b[0] != 'N' {
		t.Fatalf("the new mapping reads %q, want the other mapping's write", second.b[0])
	}
}

func TestTruncateClearsMappedPagesPastTheNewSize(t *testing.T) {
	m := testFS()
	mem := &fakeMemory{b: make([]byte, 2*pageSize)}
	f, _ := mapFile(t, m, mem, "f", mmapShared|mmapWritable)
	if errno := f.Truncate(pageSize); errno != 0 {
		t.Fatal(errno)
	}
	if errno := f.Truncate(2 * pageSize); errno != 0 {
		t.Fatal(errno)
	}
	if mem.b[pageSize-1] != 'a' || mem.b[pageSize] != 0 {
		t.Fatalf("mapping holds %q and %q around the truncation point", mem.b[pageSize-1], mem.b[pageSize])
	}
	m.endInstance(false)
	if f.in.data[pageSize] != 0 {
		t.Fatalf("the truncated page came back as %q", f.in.data[pageSize])
	}
}

func TestGuestSyscallShimLocksAndMaps(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a wasip1 guest against the syscall overlay")
	}
	dir := t.TempDir()
	replace := map[string]string{}
	if err := writeSyscallOverlay(dir, replace); err != nil {
		t.Fatal(err)
	}
	overlay, err := writeOverlayFile(dir, replace)
	if err != nil {
		t.Fatal(err)
	}
	wasm := filepath.Join(dir, "shimguest.wasm")
	build := exec.Command("go", "build", "-overlay", overlay, "-o", wasm, "./testdata/shimguest")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, out)
	}
	cfg := defaultConfig()
	cfg.Workload = NodeSpec{Name: "client", Wasm: wasm}
	cfg.Duration = Duration(time.Second)
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.rt.Close(context.Background()) })
	if res := r.Run(1, nil); res.Failed {
		t.Fatalf("guest failed: %s\n%s", res.Reason, strings.Join(res.Trace, "\n"))
	}
}

func TestWidenIgnoresPackageTextInTheLicenseComment(t *testing.T) {
	p := packageDir(t, map[string]string{
		"lock_unix.go": "/*\npackage notes\n*/\n//go:build linux\n\npackage p\n\nimport \"syscall\"\n\nfunc Lock(fd int) error { return syscall.Flock(fd, 2) }\n",
	})
	widened, err := widen(p, nil)
	if err != nil || len(widened) != 1 {
		t.Fatalf("widened %d files, err %v", len(widened), err)
	}
	if strings.Contains(string(widened[0].source), "//go:build linux") {
		t.Fatalf("the old constraint survived:\n%s", widened[0].source)
	}
}

func TestWidenFollowsRenamedImports(t *testing.T) {
	p := packageDir(t, map[string]string{
		"lock_unix.go": "//go:build linux\n\npackage p\n\nimport sys \"golang.org/x/sys/unix\"\n\nfunc Lock(fd int) error { return sys.Flock(fd, sys.LOCK_EX) }\n",
	})
	if widened, err := widen(p, nil); err != nil || len(widened) != 1 {
		t.Fatalf("widened %d files, err %v, want the renamed import found", len(widened), err)
	}
}
