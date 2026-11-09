# A crash while a snapshot is being written leaves a temporary directory that makes every later start fail

**Upstream:** not filed. The GitHub repository is no longer public, so there is nowhere to report it; v0.2.0 is still served by proxy.golang.org.

**Repo:** jmsadair/raft (tested on v0.2.0, the latest release, commit `8dd4cdc`; the GitHub repository is no longer public, the module is still served by proxy.golang.org)
**Severity:** low. One ordinary crash (`kill -9`, OOM kill, power loss) during a snapshot makes the next start fail with a misleading error. A second start succeeds. No torn writes or strict-POSIX assumptions needed; reproduced on a real filesystem.

## Summary

Snapshots are written into a temporary directory, `snapshots/tmp-snapshotNNN/` holding `snapshot.bin` and `metadata.json`, which is renamed to `snapshot-<timestamp>` when complete (`snapshot_storage.go:191-231` and `:99-140`). If the process dies before the rename, the temporary directory stays behind. `NewSnapshotStorage` cleans it up on the next start with `fileutil.RemoveTmpFiles` (`snapshot_storage.go:183`):

```go
// internal/fileutil/fileutil.go:11-21
func RemoveTmpFiles(rootDir string) error {
	return filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasPrefix(info.Name(), "tmp") {
			return nil
		}
		return os.RemoveAll(path)
	})
}
```

`filepath.Walk` reads a directory's names before calling the walk function on it. The callback removes `tmp-snapshotNNN` with everything inside it and returns `nil`, so Walk goes on to `lstat` the children it already listed. That fails with "no such file or directory", Walk passes the error to the callback, and the callback returns it:

```
could not remove temporary files: lstat data/snapshots/tmp-snapshot1196913385/metadata.json: no such file or directory
```

By then the directory has been deleted, but `NewRaft` has already failed. A second start succeeds, so a supervisor that restarts the process on failure recovers. A process that is started once (an operator script, or a deployment that treats a failed start as fatal) stays down, and the error points at a file that no longer exists.

The same helper is used for `log/` (`log.go:191`) and `state/` (`state_storage.go:102`), but those only ever contain temporary files, never directories, so only the snapshot directory triggers it.

## Reproduction

On a real filesystem:

```go
dir, _ := os.MkdirTemp("", "jms")
tmp := filepath.Join(dir, "snapshots", "tmp-snapshot42")
os.MkdirAll(tmp, 0o755)
os.WriteFile(filepath.Join(tmp, "snapshot.bin"), []byte("x"), 0o644)
os.WriteFile(filepath.Join(tmp, "metadata.json"), []byte("{}"), 0o644)
_, err := raft.NewSnapshotStorage(dir)
// could not remove temporary files: lstat .../snapshots/tmp-snapshot42/metadata.json: no such file or directory
```

With [seedfall](https://github.com/kunalgoesbyken/seedfall), on a 3-node key-value store that snapshots every 32 entries:

```
cd examples/jmsraft
seedfall replay -c seedfall.json -seed 16 -faults tmpdir.faults.json
```

One crash, while n3 is writing a snapshot:

```
    0.898s  fault crash n3#1 during disk op 170, restart at 1.286s
    1.286s  start n3#2
    1.286s  FAIL  n3#2: node restarts from its own disk: could not remove temporary files: lstat data/snapshots/tmp-snapshot2799775943/metadata.json: No such file or directory
```

## Suggested fix

Skip a directory after removing it:

```go
if err := os.RemoveAll(path); err != nil {
	return err
}
if info.IsDir() {
	return filepath.SkipDir
}
return nil
```

**Fix verified:** with this change applied to a local copy of v0.2.0 (together with the [state file](jmsadair-raft-state-file-fsync.md) and [torn log tail](jmsadair-raft-log-torn-tail.md) fixes), `seedfall.json` passes 150 seeds of crashes, partitions, torn writes and crashes during disk I/O, linearizable, and the reproduction above succeeds.
