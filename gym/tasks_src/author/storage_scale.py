from authorlib import E, bug_patches, edit, gofmt, read_tree, write_patch
from liveness import DEDUPE_ON, REDIAL_ON

F = "server/main.go"

SYMSWAP_FULL = [
    E(F, '''
    mu      sync.Mutex
    dir     string
    gen     int
    values  map[string]string
    syncDir bool
}
''', '''
    mu     sync.Mutex
    dir    string
    gen    int
    values map[string]string
}
'''),
    E(F, '''    syncDirFirst := flag.Bool("sync-dir", false, "fsync the directory after creating a generation, before pointing the symlink at it")\n''', ""),
    E(F, ", syncDir: *syncDirFirst}", "}"),
    E(F, '''
    if s.syncDir {
        if err := fsyncDir(s.dir); err != nil {
            return err
        }
    }
''', '''
    if err := fsyncDir(s.dir); err != nil {
        return err
    }
'''),
]

SYMSWAP_BUGS = {
    "symswap-gen-unsynced": [
        E(F, '''
    if _, err := f.WriteString(content); err != nil {
        return err
    }
    return f.Sync()
''', '''
    if _, err := f.WriteString(content); err != nil {
        return err
    }
    return nil
'''),
    ],
    "symswap-no-publish-sync": [
        E(F, '''
    if err := os.Rename(tmp, filepath.Join(s.dir, currentLink)); err != nil {
        return err
    }
    if err := fsyncDir(s.dir); err != nil {
        return err
    }
''', '''
    if err := os.Rename(tmp, filepath.Join(s.dir, currentLink)); err != nil {
        return err
    }
'''),
    ],
    "symswap-stale-tmp-link": [
        E(F, "    os.Remove(tmp)\n", ""),
    ],
    "symswap-fsync-wrong-dir": [
        E(F, '''
    if err := fsyncDir(s.dir); err != nil {
        return err
    }
    tmp := filepath.Join(s.dir, "current.tmp")
''', '''
    if err := fsyncDir(filepath.Dir(s.dir)); err != nil {
        return err
    }
    tmp := filepath.Join(s.dir, "current.tmp")
'''),
        E(F, '''
    if err := fsyncDir(s.dir); err != nil {
        return err
    }
    if next > 2 {
''', '''
    if err := fsyncDir(filepath.Dir(s.dir)); err != nil {
        return err
    }
    if next > 2 {
'''),
    ],
    "symswap-nonatomic-publish": [
        E(F, '''
    tmp := filepath.Join(s.dir, "current.tmp")
    os.Remove(tmp)
    if err := os.Symlink(name, tmp); err != nil {
        return err
    }
    if err := os.Rename(tmp, filepath.Join(s.dir, currentLink)); err != nil {
        return err
    }
''', '''
    link := filepath.Join(s.dir, currentLink)
    os.Remove(link)
    if err := os.Symlink(name, link); err != nil {
        return err
    }
'''),
    ],
    "symswap-gc-before-publish": [
        E(F, '''
    if err := os.Rename(tmp, filepath.Join(s.dir, currentLink)); err != nil {
        return err
    }
    if err := fsyncDir(s.dir); err != nil {
        return err
    }
    if next > 2 {
        os.Remove(filepath.Join(s.dir, fmt.Sprintf("gen.%d", next-2)))
    }
''', '''
    if err := os.Rename(tmp, filepath.Join(s.dir, currentLink)); err != nil {
        return err
    }
    if next > 1 {
        os.Remove(filepath.Join(s.dir, fmt.Sprintf("gen.%d", next-1)))
    }
    if err := fsyncDir(s.dir); err != nil {
        return err
    }
'''),
    ],
}

SYMSWAP_CHEAT = [E(F, 'if err := s.put(fields[1], fields[2]); err != nil {', 'if err := fmt.Errorf("writes are disabled"); err != nil {')]

LEDGER_FULL = [
    E(F, "    total int\n    sync  bool\n}", "    total int\n}"),
    E(F, '''    syncWrites := flag.Bool("fsync", true, "fsync every entry before acknowledging it")\n''', ""),
    E(F, "open(*path, *syncWrites)", "open(*path)"),
    E(F, "func open(path string, syncWrites bool) (*ledger, error) {", "func open(path string) (*ledger, error) {"),
    E(F, ", total: total, sync: syncWrites}", ", total: total}"),
    E(F, '''
    if l.sync {
        if err := l.file.Sync(); err != nil {
            return "ERR"
        }
    }
''', '''
    if err := l.file.Sync(); err != nil {
        return "ERR"
    }
'''),
]

LEDGER_BUGS = {
    "ledger-torn-tail-kept": [
        E(F, "os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)", "os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)"),
        E(F, '''
    if err := f.Truncate(int64(complete)); err != nil {
        return nil, err
    }
    if _, err := f.Seek(int64(complete), 0); err != nil {
        return nil, err
    }
''', ""),
    ],
    "ledger-sync-before-write": [
        E(F, '''
    if _, err := fmt.Fprintf(l.file, "%d\\n", n); err != nil {
        return "ERR"
    }
    if err := l.file.Sync(); err != nil {
        return "ERR"
    }
''', '''
    if err := l.file.Sync(); err != nil {
        return "ERR"
    }
    if _, err := fmt.Fprintf(l.file, "%d\\n", n); err != nil {
        return "ERR"
    }
'''),
    ],
}

LEDGER_CHEAT = [E(F, "    n, err := strconv.Atoi(cmd[1])\n", '''
    return "ERR"
    n, err := strconv.Atoi(cmd[1])
''')]

HTTPKV_FULL = [
    E(F, "type store struct {\n    sync   bool\n    mu     sync.Mutex\n", "type store struct {\n    mu     sync.Mutex\n"),
    E(F, '''    fsync := flag.Bool("fsync", true, "fsync before acknowledging a write")\n    flag.Parse()\n''', "    flag.Parse()\n"),
    E(F, "    s.sync = *fsync\n", ""),
    E(F, '''
func (s *store) persist() error {
    if !s.sync {
        return nil
    }
    return s.file.Sync()
}

''', ""),
    E(F, "if err := s.persist(); err != nil {", "if err := s.file.Sync(); err != nil {"),
]

HTTPKV_BUGS = {
    "httpkv-no-dir-sync": [
        E(F, '''
    for _, d := range []string{dir, "."} {
        if err := syncDir(d); err != nil {
            return nil, err
        }
    }
''', ""),
    ],
    "httpkv-sync-before-write": [
        E(F, '''
    if _, err := io.WriteString(s.file, line); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    if err := s.file.Sync(); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
''', '''
    if err := s.file.Sync(); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    if _, err := io.WriteString(s.file, line); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
'''),
    ],
    "httpkv-torn-tail-kept": [
        E(F, "values, complete := replay(data)", "values, _ := replay(data)"),
        E(F, "os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)", "os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)"),
        E(F, '''
    if err := f.Truncate(int64(complete)); err != nil {
        return nil, err
    }
    if _, err := f.Seek(int64(complete), io.SeekStart); err != nil {
        return nil, err
    }
''', ""),
    ],
}

HTTPKV_CHEAT = [E(F, "    line, ok := record(k, v)\n", '''
    http.Error(w, "writes are disabled", http.StatusServiceUnavailable)
    return
    line, ok := record(k, v)
''')]

LIVENESS_FULL = REDIAL_ON

LIVENESS_BUGS = {
    "liveness-log-unsynced": [
        E(F, '''
    if err := s.log.Sync(); err != nil {
        return "ERR " + err.Error()
    }
''', ""),
    ],
    "liveness-applied-not-recovered": [
        E(F, '''
    for _, id := range strings.Fields(string(data)) {
        s.applied[id] = true
        s.value++
    }
''', '''
    for range strings.Fields(string(data)) {
        s.value++
    }
'''),
    ],
    "liveness-apply-before-replicate": [
        E(F, '''
    if s.backup != nil {
        if err := s.backup.forward(id, timeout); err != nil {
            return "ERR " + err.Error()
        }
    }
    if _, err := s.log.WriteString(id + "\\n"); err != nil {
        return "ERR " + err.Error()
    }
    if err := s.log.Sync(); err != nil {
        return "ERR " + err.Error()
    }
    s.applied[id] = true
    s.value++
    return fmt.Sprint(s.value)
''', '''
    if _, err := s.log.WriteString(id + "\\n"); err != nil {
        return "ERR " + err.Error()
    }
    if err := s.log.Sync(); err != nil {
        return "ERR " + err.Error()
    }
    s.applied[id] = true
    s.value++
    if s.backup != nil {
        if err := s.backup.forward(id, timeout); err != nil {
            return "ERR " + err.Error()
        }
    }
    return fmt.Sprint(s.value)
'''),
    ],
    "liveness-backup-error-ignored": [
        E(F, '''
    if s.backup != nil {
        if err := s.backup.forward(id, timeout); err != nil {
            return "ERR " + err.Error()
        }
    }
''', '''
    if s.backup != nil {
        s.backup.forward(id, timeout)
    }
'''),
    ],
}

LIVENESS_CHEAT = [E(F, '''    if cmd != "ADD" {\n        return fmt.Sprint(s.value)\n    }\n''', '''
    if cmd != "ADD" {
        return fmt.Sprint(s.value)
    }
    if cmd == "ADD" {
        return "ERR writes are disabled"
    }
''')]


METAPAGE_FULL = [
    E(F, '''
    seq      uint64
    size     int64
    syncBody bool
}
''', '''
    seq    uint64
    size   int64
}
'''),
    E(F, '''
    mu       sync.Mutex
    file     *os.File
    values   map[string]string
''', '''
    mu     sync.Mutex
    file   *os.File
    values map[string]string
'''),
    E(F, '''    syncBody := flag.Bool("sync-body", false, "fsync the record before writing the meta slot that commits it")\n''', ""),
    E(F, "    s.syncBody = *syncBody\n", ""),
    E(F, '''
    if s.syncBody {
        if err := s.file.Sync(); err != nil {
            return err
        }
    }
''', '''
    if err := s.file.Sync(); err != nil {
        return err
    }
'''),
]

METAPAGE_BUGS = {
    "metapage-single-meta-slot": [
        E(F, "int64(seq%2)*slotSize", "0"),
    ],
    "metapage-slot-crc-unchecked": [
        E(F, '''
        if crc32.ChecksumIEEE(slot[:16]) != binary.LittleEndian.Uint32(slot[16:]) {
            continue
        }
''', ""),
    ],
    "metapage-append-offset-off-by-one": [
        E(F, "metaSize+s.size)", "metaSize+s.size-1)"),
    ],
    "metapage-meta-unsynced": [
        E(F, '''
    if _, err := s.file.WriteAt(encodeSlot(seq, size), int64(seq%2)*slotSize); err != nil {
        return err
    }
    if err := s.file.Sync(); err != nil {
        return err
    }
''', '''
    if _, err := s.file.WriteAt(encodeSlot(seq, size), int64(seq%2)*slotSize); err != nil {
        return err
    }
'''),
    ],
}

METAPAGE_CHEAT = [E(F, "func (s *store) handle(cmd []string) string {\n", '''
func (s *store) handle(cmd []string) string {
    if len(cmd) > 0 && cmd[0] == "PUT" {
        return "ERR writes are disabled\\n"
    }
''')]


def chain(name, example, prefix, full, bugs, cheat, base_patch):
    pristine = read_tree(example, prefix)
    start = pristine if base_patch is None else base_patch(pristine)
    correct = gofmt(edit(start, full))
    if full:
        write_patch(name + "-full", start, correct)
    for task, edits in bugs.items():
        bug_patches(task, correct, gofmt(edit(correct, edits)), cheat)


def run():
    chain("symswap", "examples/symswap/server", "server", SYMSWAP_FULL, SYMSWAP_BUGS, SYMSWAP_CHEAT, None)
    chain("ledger", "examples/ledger/server", "server", LEDGER_FULL, LEDGER_BUGS, LEDGER_CHEAT, None)
    chain("httpkv", "examples/httpkv/server", "server", HTTPKV_FULL, HTTPKV_BUGS, HTTPKV_CHEAT, None)
    chain("metapage", "examples/metapage/server", "server", METAPAGE_FULL, METAPAGE_BUGS, METAPAGE_CHEAT, None)
    chain("liveness", "examples/liveness/server", "server", LIVENESS_FULL, LIVENESS_BUGS, LIVENESS_CHEAT, lambda t: edit(t, DEDUPE_ON))


if __name__ == "__main__":
    run()
