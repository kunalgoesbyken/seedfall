from authorlib import drop, edit, read_tree, write_patch

CHEAT_KV_HANDLE = (
    "server/main.go",
    "func (s *store) handle(cmd []string) string {\n",
    'func (s *store) handle(cmd []string) string {\n\tif len(cmd) > 0 && cmd[0] == "PUT" {\n\t\treturn "ERR writes are disabled\\n"\n\t}\n',
)


def metapage():
    base = read_tree("examples/metapage/server", "server")
    buggy = edit(base, [
        ("server/main.go", "\tseq      uint64\n\tsize     int64\n\tsyncBody bool\n}", "\tseq    uint64\n\tsize   int64\n}"),
        ("server/main.go", "\tmu       sync.Mutex\n\tfile     *os.File\n\tvalues   map[string]string\n", "\tmu     sync.Mutex\n\tfile   *os.File\n\tvalues map[string]string\n"),
        ("server/main.go", '\tsyncBody := flag.Bool("sync-body", false, "fsync the record before writing the meta slot that commits it")\n', ""),
        ("server/main.go", "\ts.syncBody = *syncBody\n", ""),
        ("server/main.go", "\tif s.syncBody {\n\t\tif err := s.file.Sync(); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", ""),
    ])
    write_patch("metapage-buggy", base, buggy)
    fixed = edit(buggy, [
        ("server/main.go", "\tseq, size := s.seq+1, s.size+int64(len(rec))\n", "\tif err := s.file.Sync(); err != nil {\n\t\treturn err\n\t}\n\tseq, size := s.seq+1, s.size+int64(len(rec))\n"),
    ])
    write_patch("metapage-fix", buggy, fixed)
    write_patch("metapage-cheat", buggy, edit(buggy, [CHEAT_KV_HANDLE]))


def symswap():
    base = read_tree("examples/symswap/server", "server")
    buggy = edit(base, [
        ("server/main.go", "\tmu      sync.Mutex\n\tdir     string\n\tgen     int\n\tvalues  map[string]string\n\tsyncDir bool\n}", "\tmu     sync.Mutex\n\tdir    string\n\tgen    int\n\tvalues map[string]string\n}"),
        ("server/main.go", '\tsyncDirFirst := flag.Bool("sync-dir", false, "fsync the directory after creating a generation, before pointing the symlink at it")\n', ""),
        ("server/main.go", ", syncDir: *syncDirFirst}", "}"),
        ("server/main.go", "\tif s.syncDir {\n\t\tif err := fsyncDir(s.dir); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", ""),
    ])
    write_patch("symswap-buggy", base, buggy)
    fixed = edit(buggy, [
        ("server/main.go", '\ttmp := filepath.Join(s.dir, "current.tmp")\n', '\tif err := fsyncDir(s.dir); err != nil {\n\t\treturn err\n\t}\n\ttmp := filepath.Join(s.dir, "current.tmp")\n'),
    ])
    write_patch("symswap-fix", buggy, fixed)
    write_patch("symswap-cheat", buggy, edit(buggy, [
        ("server/main.go", "if err := s.put(fields[1], fields[2]); err != nil {", 'if err := fmt.Errorf("writes are disabled"); err != nil {'),
    ]))


def httpkv():
    base = read_tree("examples/httpkv/server", "server")
    buggy = edit(base, [
        ("server/main.go", "type store struct {\n\tsync   bool\n\tmu     sync.Mutex\n", "type store struct {\n\tmu     sync.Mutex\n"),
        ("server/main.go", '\tfsync := flag.Bool("fsync", true, "fsync before acknowledging a write")\n\tflag.Parse()\n', "\tflag.Parse()\n"),
        ("server/main.go", "\ts.sync = *fsync\n", ""),
        ("server/main.go", "\tif err := s.persist(); err != nil {\n\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)\n\t\treturn\n\t}\n", ""),
        ("server/main.go", "func (s *store) persist() error {\n\tif !s.sync {\n\t\treturn nil\n\t}\n\treturn s.file.Sync()\n}\n\n", ""),
    ])
    write_patch("httpkv-buggy", base, buggy)
    fixed = edit(buggy, [
        ("server/main.go", "\tif v == nil {\n\t\tdelete(s.values, k)", "\tif err := s.file.Sync(); err != nil {\n\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)\n\t\treturn\n\t}\n\tif v == nil {\n\t\tdelete(s.values, k)"),
    ])
    write_patch("httpkv-fix", buggy, fixed)
    write_patch("httpkv-cheat", buggy, edit(buggy, [
        ("server/main.go", "\tline, ok := record(k, v)\n", '\thttp.Error(w, "writes are disabled", http.StatusServiceUnavailable)\n\treturn\n\tline, ok := record(k, v)\n'),
    ]))
    client = read_tree("examples/httpkv/client", "client")
    write_patch("httpkv-client-ack", client, edit(client, [
        ("client/main.go", "\t\t\tacked = append(acked, e)\n", '\t\t\tacked = append(acked, e)\n\t\t\tsim.Event("gym.ack", e.key)\n'),
    ]))


def ledger():
    base = read_tree("examples/ledger/server", "server")
    buggy = edit(base, [
        ("server/main.go", "\ttotal int\n\tsync  bool\n}", "\ttotal int\n}"),
        ("server/main.go", '\tsyncWrites := flag.Bool("fsync", true, "fsync every entry before acknowledging it")\n', ""),
        ("server/main.go", "open(*path, *syncWrites)", "open(*path)"),
        ("server/main.go", "func open(path string, syncWrites bool) (*ledger, error) {", "func open(path string) (*ledger, error) {"),
        ("server/main.go", ", total: total, sync: syncWrites}", ", total: total}"),
        ("server/main.go", "\tif l.sync {\n\t\tif err := l.file.Sync(); err != nil {\n\t\t\treturn \"ERR\"\n\t\t}\n\t}\n", ""),
    ])
    write_patch("ledger-buggy", base, buggy)
    fixed = edit(buggy, [
        ("server/main.go", "\tl.total += n\n", '\tif err := l.file.Sync(); err != nil {\n\t\treturn "ERR"\n\t}\n\tl.total += n\n'),
    ])
    write_patch("ledger-fix", buggy, fixed)
    write_patch("ledger-cheat", buggy, edit(buggy, [
        ("server/main.go", "\tn, err := strconv.Atoi(cmd[1])\n", '\treturn "ERR"\n\tn, err := strconv.Atoi(cmd[1])\n'),
    ]))
    client = read_tree("examples/ledger/client", "client")
    write_patch("ledger-client-ack", client, edit(client, [
        ("client/main.go", "\t\t\tacked += amount\n", '\t\t\tacked += amount\n\t\t\tlifecycle.SendEvent("gym.ack", map[string]any{"amount": amount})\n'),
    ]))


if __name__ == "__main__":
    metapage()
    symswap()
    httpkv()
    ledger()
