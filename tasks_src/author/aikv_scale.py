from aikv import single_impl
from authorlib import E, bug_patches, edit, gofmt, read_tree

ALIASES = {"haiku-2": "haiku2", "haiku-3": "haiku3", "opus-1": "opus1", "opus-3": "opus3", "sonnet-1": "sonnet1", "sonnet-2": "sonnet2", "sonnet-3": "sonnet3"}

BUGS = {
    "aikv-opus-3-snapshot-unsynced": ("opus-3", "kv.go", "ErrClosed", [
        E("impl/opus-3/kv.go", '''
    if err := f.Sync(); err != nil {
        return nil, 0, err
    }
    if err := f.Close(); err != nil {
        return nil, 0, err
    }
    return index, off, nil
''', '''
    if err := f.Close(); err != nil {
        return nil, 0, err
    }
    return index, off, nil
'''),
    ]),
    "aikv-sonnet-2-commit-before-data-sync": ("sonnet-2", "kv.go", "ErrClosed", [
        E("impl/sonnet-2/compact.go", '''
    if err := w.Flush(); err != nil {
        return err
    }
    if err := f.Sync(); err != nil {
        return err
    }
    db.hit("compact-data-synced")
''', '''
    if err := w.Flush(); err != nil {
        return err
    }
    db.hit("compact-data-synced")
'''),
    ]),
    "aikv-sonnet-1-base-unsynced": ("sonnet-1", "db.go", "ErrClosed", [
        E("impl/sonnet-1/compact.go", '''
    if err := syncFile(f); err != nil {
        return err
    }
    if err := f.Close(); err != nil {
        return err
    }
    db.fire("tmp-written")
''', '''
    if err := f.Close(); err != nil {
        return err
    }
    db.fire("tmp-written")
'''),
    ]),
    "aikv-opus-1-fsync-error-ignored": ("opus-1", "kv.go", "ErrClosed", [
        E("impl/opus-1/kv.go", '''
    if err := db.f.Sync(); err != nil {
        return db.fail(err)
    }
    db.mu.Lock()
    db.size = off + int64(len(rec))
''', '''
    db.f.Sync()
    db.mu.Lock()
    db.size = off + int64(len(rec))
'''),
    ]),
    "aikv-haiku-3-fsync-error-ignored": ("haiku-3", "db.go", "ErrClosed", [
        E("impl/haiku-3/db.go", '''
    if err := db.f.Sync(); err != nil {
        return db.fail(err)
    }
    db.size += int64(len(buf))
''', '''
    db.f.Sync()
    db.size += int64(len(buf))
'''),
    ]),
    "aikv-sonnet-3-compact-unsynced": ("sonnet-3", "kv.go", "ErrClosed", [
        E("impl/sonnet-3/compact.go", '''
    if err == nil {
        err = out.Sync()
    }
''', ""),
    ]),
}


DROPPED = {
    "aikv-haiku-2-compact-unsynced": ("haiku-2", "kv.go", "ErrTooLarge", [
        E("impl/haiku-2/compact.go", '''
    keys, size, err := copyLive(db, f)
    if err == nil {
        err = f.Sync()
    }
''', '''
    keys, size, err := copyLive(db, f)
'''),
    ]),
    "aikv-opus-3-active-dir-unsynced": ("opus-3", "kv.go", "ErrClosed", [
        E("impl/opus-3/kv.go", '''
    if err := f.Sync(); err != nil {
        f.Close()
        return err
    }
    if err := syncDir(db.dir); err != nil {
        f.Close()
        return err
    }
    db.files[id] = f
''', '''
    if err := f.Sync(); err != nil {
        f.Close()
        return err
    }
    db.files[id] = f
'''),
    ]),
}


def write_signatures(impl, file):
    text = read_tree("examples/aikv/impl/" + impl, "impl/" + impl)["impl/%s/%s" % (impl, file)]
    found = [line + "\n" for line in text.split("\n") if line.startswith(("func (db *DB) Put(", "func (db *DB) Delete("))]
    if len(found) != 2:
        raise SystemExit("expected Put and Delete in %s/%s, found %d" % (impl, file, len(found)))
    return found


def run():
    for impl, alias in ALIASES.items():
        single_impl(impl, alias)
    for task, (impl, file, error, edits) in {**BUGS, **DROPPED}.items():
        correct = read_tree("examples/aikv/impl/" + impl, "impl/" + impl)
        buggy = gofmt(edit(correct, edits))
        cheat = [("impl/%s/%s" % (impl, file), sig, sig + "\treturn %s\n" % error) for sig in write_signatures(impl, file)]
        bug_patches(task, correct, buggy, cheat)


if __name__ == "__main__":
    run()
