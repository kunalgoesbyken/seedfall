# Snapshots are ordered by wall-clock time, so after the clock steps back a restart loads an old snapshot and the node never starts again

**Upstream:** not filed. The GitHub repository is no longer public, so there is nowhere to report it; v0.2.0 is still served by proxy.golang.org.

**Repo:** jmsadair/raft (tested on v0.2.0, the latest release, commit `8dd4cdc`; the GitHub repository is no longer public, the module is still served by proxy.golang.org)
**Severity:** a backward wall-clock step (an NTP step, a VM restored from a snapshot, a manual clock fix) followed by one snapshot and one restart leaves a node that fails `NewRaft` on every start. No disk faults needed.

## Summary

Each snapshot directory is named after the wall clock when it is created (`snapshot_storage.go:228`, `:300-303`):

```go
func buildDirectoryBase() string {
	now := time.Now().UnixNano()
	return fmt.Sprintf("snapshot-%v", now)
}
```

On start, `SnapshotFile` (`snapshot_storage.go:235-266`) loads the directory that sorts last (`dirNames[len(dirNames)-1]`, line 244), on the assumption that the last name is the newest snapshot. Old snapshots are never deleted, so every snapshot the node has taken is a candidate.

When the wall clock steps back, the next snapshot gets a smaller name than the snapshots taken before the step. `takeSnapshot` (`raft.go:1515-1576`) then compacts the log up to that newest snapshot. On the next start, `restore` (`raft.go:309-377`) loads an older snapshot, sets `lastIncludedIndex` from it, and walks the log from there to find the latest configuration. The log no longer contains those entries:

```
could not get entry from log: could not get entry: index 33 does not exist
```

`NewRaft` fails on every start until a snapshot with a larger name exists, which cannot happen because the node cannot start. Had the walk not failed, the node would have restored an old state machine with a log that skips the entries in between.

A side note: `directories()` (`snapshot_storage.go:269-297`) tries to sort numerically with `fmt.Sscanf(dirNames[i], "snapshot-%d", ...)`, but `dirNames` holds full paths (`data/snapshots/snapshot-...`), so the scan always fails and every key is 0. The order actually comes from `os.ReadDir`'s lexical order, which happens to match numeric order for 19-digit nanosecond timestamps.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall): a 3-node key-value store on jmsadair/raft v0.2.0 that snapshots every 32 entries, with crashes and wall-clock jumps only (no disk faults):

```
cd examples/jmsraft
seedfall replay -c clock.json -seed 13 -faults snapshotorder.faults.json
```

```
    0.981s  fault n3's wall clock jumps -4.856s
    1.837s  fault crash n2#1, restart at 2.711s
    2.107s  fault crash n3#1, restart at 2.684s
    2.684s  start n3#2
    2.684s  FAIL  n3#2: node restarts from its own disk: could not get entry from log: could not get entry: index 33 does not exist
```

n3's clock steps back about 5 seconds, n3 takes another snapshot and compacts its log, and its next start loads a snapshot from before the step. The same failure appeared on its own in a sweep with process pauses and clock jumps but no crashes (`pause.lease.json`, seed 11), after the node had exited for an unrelated reason.

## Suggested fix

Order snapshots by what they contain, not by when they were written. For example, pick the directory whose metadata has the highest `LastIncludedIndex`:

```go
func latestSnapshot(dirNames []string) (string, SnapshotMetadata, error) {
	var latest string
	var latestMetadata SnapshotMetadata
	for _, dirName := range dirNames {
		metadata, err := readMetadata(dirName)
		if err != nil {
			return "", SnapshotMetadata{}, err
		}
		if latest == "" || metadata.LastIncludedIndex >= latestMetadata.LastIncludedIndex {
			latest, latestMetadata = dirName, metadata
		}
	}
	return latest, latestMetadata, nil
}
```

Naming directories by index and term instead of time would also work. Deleting older snapshots once a newer one is durable would bound disk usage too, which currently grows with every snapshot.

**Fix verified:** with this change applied to a local copy of v0.2.0 (together with the three crash-recovery fixes: [state file](jmsadair-raft-state-file-fsync.md), [torn log tail](jmsadair-raft-log-torn-tail.md), [temporary snapshot directory](jmsadair-raft-tmp-snapshot-dir.md)), the schedule above passes, and so do 100 seeds each of `clock.json` (crashes and clock jumps), `pause.json` and `pause.lease.json` (process pauses and clock jumps). On the stock library, `pause.lease.json` fails on seed 11 with this bug.
