# WriteStream with sync and TempDir doesn't fsync the directory after the rename, so an acknowledged write can revert after a crash

**Upstream:** [peterbourgon/diskv#83](https://github.com/peterbourgon/diskv/issues/83)

**Repo:** peterbourgon/diskv (tested on v3.0.1, which is the latest commit on `master`, from 2021)
**Severity:** low. It needs a filesystem where a rename is durable only after the parent directory is fsynced, plus a crash shortly after the write. On such filesystems, an acknowledged synced write can silently revert to the previous value, or disappear if it created the key.

## Summary

With `Options.TempDir` set, `writeStreamWithLock` writes the value to a temporary file (diskv.go:206), fsyncs it when `sync` is true (diskv.go:260-261), and renames it over the key's file (diskv.go:274). The parent directory of the key file is never fsynced, and neither are directories created by `ensurePathWithLock` (diskv.go:622, `os.MkdirAll`). Under POSIX, the new name isn't durable until the directory is fsynced. After a crash the key can point back at the old file, or not exist at all, even though `WriteStream(key, r, true)` returned nil.

`TempDir` is documented as the way to get atomic writes, and `sync` as the way to get an explicit fsync. Together they read as the durable configuration, but the result is durable only on filesystems that order the rename with the data (ext4 with the default `data=ordered`, btrfs).

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall). It runs a small server on diskv v3.0.1 with `TempDir` set that writes with `WriteStream(key, r, true)`. It acknowledges `SET` only after the write returns. A client overwrites 40 keys 300 times and then checks that every key holds a value at least as new as the last acknowledged one. The simulated disk keeps only fsynced data across a crash, and with `strict_dirs` a directory entry survives a crash only after the directory is fsynced.

```
cd examples/diskv
seedfall replay -c seedfall.strict.json -seed 1
```

`seedfall shrink` reduces the schedule to three faults:

```
faults before the failure (3):
  292.108ms  partition [client] from the rest until 627.143ms
  757.268ms  crash server during disk op #56, restart at 1.121388s
  3.261204s  crash server during disk op #314, restart at 3.73383s
```

The client reports `acknowledged write lost: k0=v240, server now answers "VAL v200"`. The rename that installed `v240` was lost, so the key reverted to an earlier version.

| Config | Stock v3.0.1 | Patched |
|---|---|---|
| `TempDir` + sync, crashes, partitions, torn writes, crash during I/O (`seedfall.json`) | 300/300 pass | — |
| plus strict directory durability (`seedfall.strict.json`) | fails, seed 1 | 300/300 pass |
| without sync (`seedfall.nosync.json`) | fails, seed 3 (expected) | — |
| sync without `TempDir` (`seedfall.inplace.json`) | fails, seed 2 (expected: the in-place overwrite truncates first) | — |

## Suggested fix

When `sync` is true, fsync the key's directory after the rename:

```go
if sync {
	if err := syncDir(filepath.Dir(fullPath)); err != nil {
		return fmt.Errorf("dir sync: %s", err)
	}
}
```

Directories newly created by `ensurePathWithLock` need the same treatment, up to the first directory that already existed. That doesn't matter with the default flat transform.

## Fix verified

A local copy of v3.0.1 with the directory fsync after the rename passes seed 1 and 300 seeds of `seedfall.strict.json`.
