package engine

import (
	"strings"
	"testing"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
)

func weakened(m *memFS, mut Mutation) *int {
	count := 0
	m.weaken, m.onWeakened = mut, func() { count++ }
	return &count
}

func TestRenameNotAtomicLosesBothNamesUntilTheNextFsync(t *testing.T) {
	for _, strict := range []bool{false, true} {
		m := testFS()
		if strict {
			m.strict()
		}
		hits := weakened(m, RenameNotAtomic)
		old := open(t, m, "state", rw)
		write(t, old, "v1")
		fsync(t, old)
		fsync(t, open(t, m, ".", 0))
		tmp := open(t, m, "state.tmp", rw)
		write(t, tmp, "v2")
		fsync(t, tmp)
		fsync(t, open(t, m, ".", 0))
		if errno := m.Rename("state.tmp", "state"); errno != 0 {
			t.Fatal(errno)
		}
		m.crash(seeded(1), crashModel{})
		if _, ok := contents(m, "state"); ok || *hits != 1 {
			t.Fatalf("strict %v: state survived a crash inside the rename window (%d hits)", strict, *hits)
		}
		if _, ok := contents(m, "state.tmp"); ok {
			t.Fatalf("strict %v: the source name survived", strict)
		}
	}
	m := testFS()
	hits := weakened(m, RenameNotAtomic)
	f := open(t, m, "fresh.tmp", rw)
	write(t, f, "v1")
	fsync(t, f)
	m.Rename("fresh.tmp", "fresh")
	old := open(t, m, "a", rw)
	fsync(t, old)
	m.Rename("fresh", "a")
	fsync(t, old)
	m.crash(seeded(1), crashModel{})
	if got, ok := contents(m, "a"); !ok || got != "v1" || *hits != 1 {
		t.Fatalf("a rename to a new name or one closed by a later fsync was weakened: %q %v, %d hits", got, ok, *hits)
	}
}

func TestTruncateNotDurableBringsBackTheOldTail(t *testing.T) {
	m := testFS()
	hits := weakened(m, TruncateNotDurable)
	f := open(t, m, "log", rw)
	write(t, f, "abcdef")
	fsync(t, f)
	f = open(t, m, "log", rw|fsapi.O_TRUNC)
	write(t, f, "xy")
	fsync(t, f)
	if got, _ := contents(m, "log"); got != "xy" {
		t.Fatalf("before the crash the program reads %q", got)
	}
	m.crash(seeded(1), crashModel{})
	if got, _ := contents(m, "log"); got != "xycdef" || *hits != 1 {
		t.Fatalf("after the crash %q with %d hits, want the old tail back", got, *hits)
	}
}

func mutantNet(t *testing.T, mut Mutation) (*netFixture, *int) {
	f := connected(t, Range{ms(1), ms(1)})
	hits := 0
	f.w.mutation, f.w.weakened, f.w.mutantRand = mut, func() { hits++ }, streams(1, "mutant")
	f.server.node.crashable = true
	return f, &hits
}

func drain(f *netFixture) string {
	f.w.now += int64(ms(50))
	var out strings.Builder
	buf := make([]byte, 64)
	for {
		n := f.w.net.read(f.server, f.peer, buf)
		if n <= 0 {
			return out.String()
		}
		out.Write(buf[:n])
	}
}

func TestNetDropAfterCloseLosesTheLastUnreadChunk(t *testing.T) {
	f, hits := mutantNet(t, NetDropAfterClose)
	f.w.net.write(f.client, f.conn, []byte("one "))
	f.w.net.write(f.client, f.conn, []byte("two"))
	f.w.net.close(f.client, f.conn)
	if got := drain(f); got != "one " || *hits != 1 {
		t.Fatalf("peer read %q with %d hits", got, *hits)
	}
	f, hits = mutantNet(t, NetDropAfterClose)
	f.w.net.write(f.client, f.conn, []byte("read"))
	drain(f)
	f.w.net.close(f.client, f.conn)
	if *hits != 0 {
		t.Fatal("a close with nothing unread was weakened")
	}
}

func TestNetworkMutantsSkipNodesThatTakeNoFaults(t *testing.T) {
	f, hits := mutantNet(t, NetDupChunk)
	f.server.node.crashable = false
	for range 100 {
		f.w.net.write(f.client, f.conn, []byte("x"))
	}
	f.w.mutation = NetDropAfterClose
	f.w.net.close(f.client, f.conn)
	if got := drain(f); len(got) != 100 || *hits != 0 {
		t.Fatalf("read %d bytes with %d hits from a node that takes no faults", len(got), *hits)
	}
}

func TestNetDupChunkDeliversSomeWritesTwiceFromTheSeed(t *testing.T) {
	sent := func() (string, int) {
		f, hits := mutantNet(t, NetDupChunk)
		for i := range 100 {
			f.w.net.write(f.client, f.conn, []byte{byte('a' + i%26)})
		}
		return drain(f), *hits
	}
	got, hits := sent()
	again, _ := sent()
	if hits == 0 || hits > 20 || len(got) != 100+hits || got != again {
		t.Fatalf("%d duplicates, %d bytes, replay equal %v", hits, len(got), got == again)
	}
}

func TestMonotonicJumpMovesTheMonotonicClock(t *testing.T) {
	n := &node{spec: NodeSpec{Name: "a"}}
	hits := 0
	w := &world{cfg: &Config{}, nodes: []*node{n}, trace: &tracer{}, mutation: MonotonicJump, weakened: func() { hits++ }}
	w.applyFault(&Fault{Kind: FaultClockJump, Node: "a", Shift: Duration(-5e9)})
	if n.monoShift != -5e9 || n.skew != -5e9 || hits != 1 {
		t.Fatalf("mono shift %d, skew %d, hits %d", n.monoShift, n.skew, hits)
	}
	w.mutation = NoMutation
	w.applyFault(&Fault{Kind: FaultClockJump, Node: "a", Shift: Duration(2e9)})
	if n.monoShift != -5e9 || hits != 1 {
		t.Fatal("an unmutated clock jump moved the monotonic clock")
	}
}

func TestParseMutations(t *testing.T) {
	if all, err := ParseMutations(""); err != nil || len(all) != len(Mutations) {
		t.Fatalf("empty list: %v %v", all, err)
	}
	if got, err := ParseMutations("net_dup_chunk, fsync_noop"); err != nil || len(got) != 2 || got[0] != NetDupChunk || got[1] != FsyncNoop {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ParseMutations("fsync_noop,typo"); err == nil || !strings.Contains(err.Error(), "monotonic_jump") {
		t.Fatalf("typo error %v", err)
	}
}
