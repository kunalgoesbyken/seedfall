# AutoRecovery renames an unsynced copy over the last datafile, so a second crash can wipe acknowledged writes

**Repo:** prologic/bitcask on git.mills.io, module `go.mills.io/bitcask/v2` (tested on v2.2.0, which is `main` at a5ea7328ad)
**Severity:** loses acknowledged, fsynced writes, up to the whole last datafile, with `WithSyncWrites(true)` and `WithAutoRecovery(true)`.

## Summary

When the last datafile has a torn tail, `recoverDatafile` (internal/data/recover.go:40) copies every valid record into `<file>.recovered` (created at recover.go:53), closes it and renames it over the datafile (recover.go:91). The copy is never fsynced, and the directory isn't fsynced after the rename.

After the rename, the datafile name points at a new inode whose contents may exist only in the page cache. The original records, which had been fsynced and acknowledged, now live only in that unsynced copy. If the machine crashes again before writeback, the datafile comes back empty or truncated, and every acknowledged record in it is gone. With `SyncWrites`, the next `Put` fsyncs the file and closes the window. A database that is idle after a recovery, or a crash loop, stays exposed.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall). It runs a small server on bitcask v2.2.0 with `WithSyncWrites(true)`, `WithAutoRecovery(true)` and a 4 KB `MaxDatafileSize`. It acknowledges `SET` only after `Put` returns. A client writes 300 keys and then checks that every acknowledged key is still there. The simulated disk keeps only fsynced data across a crash, and with torn writes a crash can keep a partial, unsynced last write.

On stock v2.2.0 this path is masked by the `config.json` and auto-recovery bugs filed separately. With those three one-line fixes applied, `seedfall.torn.json` fails on seed 8. Shrunk:

```
faults before the failure (3):
  1.819231s  crash server during disk op #227, restart at 2.310331s
  1.825862s  partition [client] from the rest until 2.722473s
  2.633961s  crash server, restart at 2.899459s
```

1. A crash during a `Put` leaves a torn tail.
2. On restart, auto-recovery rewrites the last datafile and the server reports `recovered 109 keys`.
3. The partition keeps any `Put` from arriving, so nothing fsyncs the new file.
4. The second crash drops the unsynced copy. The server restarts with `recovered 72 keys`, and the client reports `acknowledged write lost: k73=v73, server now answers "NONE"`.

## Suggested fix

In `recoverDatafile`, fsync the copy before the rename and the directory after it:

```go
if err := fr.Sync(); err != nil {
	return false, fmt.Errorf("syncing the recovered datafile: %w", err)
}
if err := os.Rename(rPath, path); err != nil {
	return false, fmt.Errorf("removing corrupted file: %s", err)
}
if err := internal.SyncDir(dir); err != nil {
	return false, fmt.Errorf("syncing the data directory: %w", err)
}
```

`internal.SyncDir` opens the directory and calls `Sync`. Truncating the original file in place at the end of the last valid record would be simpler still, and would avoid the copy.

## Fix verified

With this fix on top of the other three, seed 8 passes, and `seedfall.torn.json` passes 300 seeds.
