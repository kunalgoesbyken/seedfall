package engine

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
)

func durable(m *memFS, p string) string { return string(m.files[p].durable) }

func TestDiskBytesShortWritesThenFailsWithNoSpace(t *testing.T) {
	m := testFS()
	m.capacity = 10
	var full []int
	m.diskFull = func(_ string, kept, size int) { full = append(full, kept, size) }
	f := open(t, m, "log", rw)
	write(t, f, "abcdef")
	n, errno := f.Write([]byte("ghijkl"))
	if n != 4 || errno != 0 {
		t.Fatalf("write past capacity = %d, %v; want a short write of 4 bytes", n, errno)
	}
	if n, errno := f.Write([]byte("x")); n != 0 || errno != fsapi.EIO || !m.noSpace {
		t.Fatalf("write on a full disk = %d, %v (no space %v); want EIO flagged as ENOSPC", n, errno, m.noSpace)
	}
	if !slices.Equal(full, []int{4, 6, 0, 1}) {
		t.Fatalf("disk full reports %v, want [4 6 0 1]", full)
	}
	if n, errno := f.Pwrite([]byte("XY"), 0); n != 2 || errno != 0 {
		t.Fatalf("overwrite on a full disk = %d, %v; overwriting needs no new space", n, errno)
	}
	if errno := f.Truncate(4); errno != 0 {
		t.Fatal(errno)
	}
	write(t, open(t, m, "other", rw), "123456")
	if got, _ := contents(m, "log"); got != "XYcd" || m.used() != 10 {
		t.Fatalf("log holds %q with %d bytes used, want XYcd and 10", got, m.used())
	}
}

func TestWriteFailureWritesNothing(t *testing.T) {
	m := testFS()
	ops := 0
	m.mutated = func() { ops++ }
	m.writeFails = func(string) fsapi.Errno { return fsapi.EIO }
	f := open(t, m, "log", rw)
	if n, errno := f.Write([]byte("data")); n != 0 || errno != fsapi.EIO {
		t.Fatalf("failing write = %d, %v", n, errno)
	}
	if got, _ := contents(m, "log"); got != "" || ops != 1 || len(m.files["log"].pending) != 0 {
		t.Fatalf("failed write left %q, %d ops, %d pending; want nothing written and one disk op", got, ops, len(m.files["log"].pending))
	}
}

func TestReadCorruptionFlipsTheBufferOnly(t *testing.T) {
	m := testFS()
	f := open(t, m, "log", rw)
	write(t, f, "hello")
	fsync(t, f)
	m.readFault = func(_ string, off int64, n int) (fsapi.Errno, int, byte) { return 0, 1, 0x20 }
	buf := make([]byte, 5)
	if n, errno := f.Pread(buf, 0); n != 5 || errno != 0 || string(buf) != "hEllo" {
		t.Fatalf("corrupted read = %d, %v, %q; want hEllo", n, errno, buf)
	}
	if got, _ := contents(m, "log"); got != "hello" || durable(m, "log") != "hello" {
		t.Fatalf("a corrupted read changed the stored data to %q", got)
	}
	if d := m.files["log"].damage; len(d) != 1 || d[0].Kind != FaultReadCorrupt || d[0].Offset != 1 {
		t.Fatalf("damage = %+v, want one read_corrupt at offset 1", d)
	}
	m.readFault = func(string, int64, int) (fsapi.Errno, int, byte) { return fsapi.EIO, 0, 0 }
	if n, errno := f.Pread(buf, 0); n != 0 || errno != fsapi.EIO {
		t.Fatalf("failing read = %d, %v", n, errno)
	}
}

func pages(fill ...byte) []byte {
	var out []byte
	for _, b := range fill {
		out = append(out, bytes.Repeat([]byte{b}, pageSize)...)
	}
	return out
}

func TestSyncDamageLosesOrMisdirectsOnePage(t *testing.T) {
	tests := []struct {
		name   string
		damage Damage
		want   []byte
	}{
		{"lost write keeps the old page", Damage{Kind: FaultLostWrite, Offset: pageSize}, pages('a', 'a', 'b')},
		{"misdirected write lands on another page", Damage{Kind: FaultMisdirected, Offset: pageSize, Target: 0}, pages('b', 'a', 'b')},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testFS()
			f := open(t, m, "db", rw)
			write(t, f, string(pages('a', 'a')))
			fsync(t, f)
			var dirty []int64
			m.syncDamage = func(_ string, d []int64, n int64) (Damage, bool) {
				dirty = d
				return tt.damage, n == 3
			}
			if _, errno := f.Pwrite(pages('b', 'b'), pageSize); errno != 0 {
				t.Fatal(errno)
			}
			fsync(t, f)
			if !slices.Equal(dirty, []int64{1, 2}) {
				t.Fatalf("dirty pages %v, want [1 2]", dirty)
			}
			if got := m.files["db"].durable; !bytes.Equal(got, tt.want) {
				t.Fatalf("durable pages start with %q %q %q", got[0], got[pageSize], got[2*pageSize])
			}
			if got, _ := contents(m, "db"); got != string(pages('a', 'b', 'b')) {
				t.Fatal("the page cache lost the acknowledged write")
			}
			if d := m.files["db"].damage; len(d) != 1 || d[0].Size != pageSize {
				t.Fatalf("damage = %+v", d)
			}
		})
	}
}

func TestLostWriteOfAnAppendLeavesZeros(t *testing.T) {
	m := testFS()
	m.syncDamage = func(_ string, dirty []int64, _ int64) (Damage, bool) {
		return Damage{Kind: FaultLostWrite, Offset: dirty[0] * pageSize}, true
	}
	f := open(t, m, "log", rw)
	write(t, f, "record")
	fsync(t, f)
	m.crash(seeded(1), crashModel{})
	if got, _ := contents(m, "log"); got != strings.Repeat("\x00", 6) {
		t.Fatalf("after the crash the log holds %q, want six zero bytes", got)
	}
}

func TestCorruptOnCrashFlipsOneBitInsideThePendingWrites(t *testing.T) {
	for seed := range uint64(30) {
		m := testFS()
		f := open(t, m, "log", rw)
		write(t, f, "durable|")
		fsync(t, f)
		write(t, f, "pending")
		damaged := m.crash(seeded(seed), crashModel{torn: true, corrupt: seeded(seed + 100)})
		got, _ := contents(m, "log")
		if !strings.HasPrefix(got, "durable|") {
			t.Fatalf("seed %d: corruption reached the synced prefix: %q", seed, got)
		}
		diff := 0
		for i := range len(got) - 8 {
			if got[8+i] != "pending"[i] {
				diff++
			}
		}
		if len(got) == 8 && len(damaged) != 0 || len(got) > 8 && (diff != 1 || len(damaged) != 1 || damaged[0].Offset < 8) {
			t.Fatalf("seed %d: log %q with %d damaged bytes, reported %+v", seed, got, diff, damaged)
		}
	}
}

func TestBitrotFlipsOneStoredBit(t *testing.T) {
	m := testFS()
	f := open(t, m, "a", rw)
	write(t, f, "aaaa")
	fsync(t, f)
	p, d, ok := m.rot(seeded(4))
	if !ok || p != "a" || d.Kind != FaultBitrot {
		t.Fatalf("rot = %q, %+v, %v", p, d, ok)
	}
	if got := durable(m, "a"); got == "aaaa" || got != string(m.files["a"].data) {
		t.Fatalf("durable %q, live %q; want the same flipped byte in both", got, m.files["a"].data)
	}
	if _, _, ok := testFS().rot(seeded(4)); ok {
		t.Fatal("rot on an empty disk reported a flip")
	}
}

func TestFsyncCorruptsMutantFlipsOnlyTheDurableCopy(t *testing.T) {
	m := testFS()
	m.weaken = FsyncCorrupts
	f := open(t, m, "log", rw)
	write(t, f, "abc")
	fsync(t, f)
	if got, _ := contents(m, "log"); got != "abc" || durable(m, "log") != "`bc" {
		t.Fatalf("live %q durable %q, want abc and `bc", got, durable(m, "log"))
	}
}

func diskWorld(spec FaultSpec, explicit bool, faults []Fault) (*world, *node) {
	w := &world{cfg: &Config{}, seed: 7, spec: spec, explicit: explicit, faults: faults, ioRand: streams(7, "io"), trace: &tracer{}}
	return w, &node{spec: NodeSpec{Name: "n1"}, crashable: true, fs: testFS(), ops: 3}
}

func TestMisdirectedWriteIsKeyedBySeedAndOpAndReplays(t *testing.T) {
	w, n := diskWorld(FaultSpec{MisdirectedWrite: 1}, false, nil)
	dirty := []int64{2, 5}
	d, ok := w.syncDamage(n, "db", dirty, 8)
	if !ok || !slices.Contains(dirty, d.Offset/pageSize) || d.Target == d.Offset || d.Target%pageSize != 0 || d.Target >= 8*pageSize {
		t.Fatalf("misdirected damage %+v, %v", d, ok)
	}
	replay, rn := diskWorld(FaultSpec{}, true, w.ioFaults)
	if again, ok := replay.syncDamage(rn, "db", dirty, 8); !ok || again != d {
		t.Fatalf("replay gave %+v, want %+v", again, d)
	}
	if _, ok := w.syncDamage(n, "db", []int64{0}, 1); ok {
		t.Fatal("a one-page file had a page misdirected")
	}
	if got := w.ioFaults[0].Describe(); !strings.Contains(got, "lands at offset") {
		t.Fatalf("description %q", got)
	}
}

func TestDiskFaultsRecordedAtRuntimeAreNotScheduleSteps(t *testing.T) {
	for _, k := range []string{FaultWriteEIO, FaultENOSPC, FaultReadEIO, FaultReadCorrupt, FaultLostWrite, FaultMisdirected} {
		if !observedAtRuntime(k) || (Fault{Kind: k}).diskText() == "" {
			t.Errorf("%s must be recorded at run time and described", k)
		}
	}
	if observedAtRuntime(FaultBitrot) {
		t.Error("bitrot is planned in time, not recorded at run time")
	}
}

func TestRewritingALostPageWritesItInFull(t *testing.T) {
	m := testFS()
	lose := true
	m.syncDamage = func(_ string, dirty []int64, _ int64) (Damage, bool) {
		return Damage{Kind: FaultLostWrite, Offset: dirty[0] * pageSize}, lose
	}
	f := open(t, m, "log", rw)
	write(t, f, "first|")
	fsync(t, f)
	lose = false
	write(t, f, "second")
	fsync(t, f)
	if got := durable(m, "log"); got != "first|second" {
		t.Fatalf("after rewriting the lost page the disk holds %q, want the whole page", got)
	}
}

func TestCrashCorruptionIsReportedOnlyForFilesThatSurvive(t *testing.T) {
	m := strictFS()
	f := open(t, m, "lost", rw)
	write(t, f, "synced")
	fsync(t, f)
	if _, errno := f.Pwrite([]byte("SYN"), 0); errno != 0 {
		t.Fatal(errno)
	}
	if damaged := m.crash(seeded(1), crashModel{corrupt: seeded(2)}); len(damaged) != 0 {
		t.Fatalf("a file whose entry the crash removed was reported damaged: %+v", damaged)
	}
}

func TestBitrotUnderAPendingWriteLeavesTheLiveCopyAlone(t *testing.T) {
	m := testFS()
	f := open(t, m, "a", rw)
	write(t, f, "a")
	fsync(t, f)
	if _, errno := f.Pwrite([]byte("a"), 0); errno != 0 {
		t.Fatal(errno)
	}
	if _, _, ok := m.rot(seeded(4)); !ok {
		t.Fatal("rot found nothing durable")
	}
	fsync(t, f)
	if got, _ := contents(m, "a"); got != "a" || durable(m, "a") != "a" {
		t.Fatalf("live %q durable %q after fsyncing a pending write over a rotted byte, want a and a", got, durable(m, "a"))
	}
}
