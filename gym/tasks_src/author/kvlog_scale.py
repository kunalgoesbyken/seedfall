from authorlib import E, bug_patches, edit, gofmt, read_tree, write_patch

F = "server/main.go"

FULL = [
    E(F, '''
    "sync"
    "time"
''', '''
    "sync"
'''),
    E(F, "const readAttempts = 5", "const readAttempts = 40"),
    E(F, '''
    mu       sync.Mutex
    file     *os.File
    values   map[string]string
    always   bool
    rewind   bool
    checksum bool
}
''', '''
    mu     sync.Mutex
    file   *os.File
    values map[string]string
}
'''),
    E(F, '''
    syncMode := flag.String("sync", "batch", "batch: fsync every 200ms, always: fsync before every ack")
    repair := flag.Bool("repair", false, "truncate a torn log tail during recovery")
    rewind := flag.Bool("rewind", false, "after a failed fsync, truncate the unsynced record instead of leaving it in the page cache")
    checksum := flag.Bool("checksum", false, "end every record with a CRC32, re-read the log when a record fails it, and treat a tail that keeps failing as torn")
''', ""),
    E(F, '''s, err := open(*dir+"/kv.log", *repair, *checksum)''', '''s, err := open(*dir + "/kv.log")'''),
    E(F, '''
    s.always, s.rewind = *syncMode == "always", *rewind
    if !s.always {
        go s.flushEvery(200 * time.Millisecond)
    }
''', ""),
    E(F, "func open(path string, repair, checksum bool) (*store, error) {", "func open(path string) (*store, error) {"),
    E(F, "parse(data, checksum)\n", "parse(data)\n"),
    E(F, "if repair && complete < len(data) {", "if complete < len(data) {"),
    E(F, "return &store{file: f, values: values, checksum: checksum}, nil", "return &store{file: f, values: values}, nil"),
    E(F, "func parse(data []byte, checksum bool) (", "func parse(data []byte) ("),
    E(F, '''
        record, ok := string(data[complete:complete+i]), true
        if checksum {
            record, ok = verify(record)
        }
''', '''
        record, ok := verify(string(data[complete : complete+i]))
'''),
    E(F, '''
    record := k + "=" + v
    if s.checksum {
        record += fmt.Sprintf("#%08x", crc32.ChecksumIEEE([]byte(record)))
    }
''', '''
    record := k + "=" + v
    record += fmt.Sprintf("#%08x", crc32.ChecksumIEEE([]byte(record)))
'''),
    E(F, '''
func (s *store) flushEvery(d time.Duration) {
    for range time.Tick(d) {
        s.mu.Lock()
        s.file.Sync()
        s.mu.Unlock()
    }
}

''', ""),
    E(F, '''
    if s.always {
        if err := s.file.Sync(); err != nil {
            return s.failedSync(end, err)
        }
    }
''', '''
    if err := s.file.Sync(); err != nil {
        return s.failedSync(end, err)
    }
'''),
    E(F, '''
    if s.rewind {
        return errors.Join(err, s.file.Truncate(end))
    }
    return err
''', '''
    return errors.Join(err, s.file.Truncate(end))
'''),
]

SYNC_FILE = E(F, '''
    if err := s.file.Sync(); err != nil {
        return s.failedSync(end, err)
    }
''', '''
    if err := syncDir(s.dir); err != nil {
        return s.failedSync(end, err)
    }
''')

BUGS = {
    "kvlog-no-dir-sync": [
        E(F, '''
    for _, d := range []string{*dir, filepath.Dir(*dir)} {
        if err := syncDir(d); err != nil {
            log.Fatal(err)
        }
    }
''', ""),
        E(F, '''    "path/filepath"
''', ""),
    ],
    "kvlog-sync-wrong-handle": [
        E(F, '''
    file   *os.File
    values map[string]string
}
''', '''
    file   *os.File
    dir    string
    values map[string]string
}
'''),
        E(F, "return &store{file: f, values: values}, nil", "return &store{file: f, dir: filepath.Dir(path), values: values}, nil"),
        SYNC_FILE,
    ],
    "kvlog-truncate-off-by-one": [
        E(F, "f.Truncate(int64(complete))", "f.Truncate(int64(complete) - 1)"),
    ],
    "kvlog-sync-before-write": [
        E(F, '''
    if _, err := s.file.WriteString(s.encode(k, v)); err != nil {
        return err
    }
    if err := s.file.Sync(); err != nil {
        return s.failedSync(end, err)
    }
''', '''
    if err := s.file.Sync(); err != nil {
        return s.failedSync(end, err)
    }
    if _, err := s.file.WriteString(s.encode(k, v)); err != nil {
        return err
    }
'''),
    ],
    "kvlog-rollback-truncates-log": [
        E(F, "return errors.Join(err, s.file.Truncate(end))", "return errors.Join(err, s.file.Truncate(0))"),
    ],
    "kvlog-write-error-ignored": [
        E(F, '''
    if _, err := s.file.WriteString(s.encode(k, v)); err != nil {
        return err
    }
''', '''
    s.file.WriteString(s.encode(k, v))
'''),
    ],
    "kvlog-no-reread": [
        E(F, '''
    var data []byte
    var values map[string]string
    var complete int
    for attempt := 1; ; attempt++ {
        var err error
        data, err = os.ReadFile(path)
        if err != nil && !os.IsNotExist(err) {
            return nil, err
        }
        var bad, tail bool
        values, complete, bad, tail = parse(data)
        if !bad || attempt == readAttempts && tail {
            break
        }
        if attempt == readAttempts {
            return nil, fmt.Errorf("%s: the record at offset %d fails its checksum in %d reads", path, complete, attempt)
        }
    }
''', '''
    data, err := os.ReadFile(path)
    if err != nil && !os.IsNotExist(err) {
        return nil, err
    }
    values, complete, _, _ := parse(data)
'''),
        E(F, "const readAttempts = 40\n\n", ""),
    ],
    "kvlog-fsync-error-swallowed": [
        E(F, '''
    if err := s.file.Sync(); err != nil {
        return s.failedSync(end, err)
    }
''', '''
    s.file.Sync()
'''),
        E(F, "end, err := s.file.Seek(0, io.SeekEnd)", "_, err := s.file.Seek(0, io.SeekEnd)"),
    ],
}

CHEAT = [E(F, "func (s *store) handle(cmd []string) string {\n", '''
func (s *store) handle(cmd []string) string {
    if len(cmd) > 0 && cmd[0] == "PUT" {
        return "ERR writes are disabled\\n"
    }
''')]


def kvlog():
    pristine = read_tree("examples/kvlog/server", "server")
    full = gofmt(edit(pristine, FULL))
    write_patch("kvlog-full", pristine, full)
    for task, edits in BUGS.items():
        bug_patches(task, full, gofmt(edit(full, edits)), CHEAT)


if __name__ == "__main__":
    kvlog()
