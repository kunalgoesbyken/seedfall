package engine

import (
	"slices"
	"testing"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
)

func strictFS() *memFS {
	m := testFS()
	m.strict()
	return m
}

func syncDir(t *testing.T, m *memFS, dir string) {
	t.Helper()
	fsync(t, open(t, m, dir, fsapi.O_RDONLY))
}

func TestStrictDirsNeedDirectoryFsync(t *testing.T) {
	tests := []struct {
		name     string
		syncDirs []string
		want     []string
	}{
		{"no directory fsync loses the new entries", nil, nil},
		{"syncing only the parent keeps the directory but not the file", []string{"."}, nil},
		{"syncing only the child cannot reach it", []string{"d"}, nil},
		{"syncing both keeps the file", []string{".", "d"}, []string{"d/f"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := strictFS()
			if errno := m.Mkdir("d", 0); errno != 0 {
				t.Fatal(errno)
			}
			f := open(t, m, "d/f", rw)
			write(t, f, "data")
			fsync(t, f)
			for _, d := range tt.syncDirs {
				syncDir(t, m, d)
			}
			m.crash(seeded(1), crashModel{})
			if got := sortedKeys(m.files); !slices.Equal(got, tt.want) {
				t.Fatalf("files after crash %v, want %v", got, tt.want)
			}
			_, kept := m.dirs["d"]
			if kept != slices.Contains(tt.syncDirs, ".") {
				t.Fatalf("directory d survived = %v with syncs %v", kept, tt.syncDirs)
			}
			if got, _ := contents(m, "d/f"); len(tt.want) > 0 && got != "data" {
				t.Fatalf("d/f holds %q", got)
			}
		})
	}
}

func TestStrictDirsRenameRevertsWithoutDirectoryFsync(t *testing.T) {
	m := strictFS()
	old := open(t, m, "cur", rw)
	write(t, old, "old")
	fsync(t, old)
	syncDir(t, m, ".")
	tmp := open(t, m, "tmp", rw)
	write(t, tmp, "new")
	fsync(t, tmp)
	if errno := m.Rename("tmp", "cur"); errno != 0 {
		t.Fatal(errno)
	}
	m.crash(seeded(1), crashModel{})
	if got, _ := contents(m, "cur"); got != "old" {
		t.Fatalf("cur holds %q after a crash before the directory fsync, want %q", got, "old")
	}
}

func TestStrictDirsTornRecoveryIsAnInOrderPrefix(t *testing.T) {
	names := []string{"a", "b", "c"}
	seen := map[int]bool{}
	for seed := range uint64(200) {
		m := strictFS()
		for _, n := range names {
			open(t, m, n, rw)
		}
		m.crash(seeded(seed), crashModel{torn: true})
		got := sortedKeys(m.files)
		if !slices.Equal(got, names[:len(got)]) {
			t.Fatalf("seed %d: recovered %v, want a prefix of %v", seed, got, names)
		}
		seen[len(got)] = true
	}
	if len(seen) != len(names)+1 {
		t.Fatalf("recovered prefix lengths %v, want every length from 0 to %d", seen, len(names))
	}
}
