# The term and vote file is renamed into place without an fsync, and one crash leaves a node that can never restart

**Upstream:** not filed. The GitHub repository is no longer public, so there is nowhere to report it; v0.2.0 is still served by proxy.golang.org.

**Repo:** jmsadair/raft (tested on v0.2.0, the latest release, commit `8dd4cdc`; the GitHub repository is no longer public, the module is still served by proxy.golang.org)
**Severity:** a single power loss or OS crash at the wrong moment leaves `state/state.bin` empty or truncated. `NewRaft` then fails on every start, so the node is down until an operator intervenes, and deleting the file to get it running again throws away its vote, which risks two leaders in one term. Two such nodes in a 3-node cluster take the cluster down. No strict-POSIX assumptions needed.

## Summary

`persistentStateStorage.SetState` (`state_storage.go:109-139`) persists the current term and vote by writing a temporary file and renaming it over `state.bin`:

```go
tmpFile, err := os.CreateTemp(p.stateDir, "tmp-state")
...
if err := encodePersistentState(tmpFile, p.state); err != nil {
...
if err := os.Rename(tmpFile.Name(), filename); err != nil {
```

The temporary file is never fsynced (and never closed, so every term change also leaks a file descriptor). A crash after the rename but before the kernel writes the data back leaves `state.bin` with a durable name and missing contents. This is the classic replace-via-rename hazard: XFS, ext4 with `noauto_da_alloc` or `data=writeback`, and most other filesystems give no ordering guarantee between the data and the rename.

On restart, `persistentStateStorage.State` (`state_storage.go:141-160`) fails:

```
could not recover state from storage: could not decode state: could not read length of protobuf message: EOF
```

The `err != io.EOF` check at `state_storage.go:148` was probably meant to tolerate an empty file, but `decodePersistentState` wraps the error with `%w`, so the comparison never matches. That is fortunate: treating the empty file as "term 0, no vote" would let the node vote twice in the same term. A partially written file fails with `unexpected EOF` instead. Either way `NewRaft` (`raft.go:301`) returns the error on every start.

`SetState` runs on every election (`becomeCandidate`, `becomeFollower` and every granted vote, through `persistTermAndVote`, `raft.go:1990`), so the window is hit regularly.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall): a 3-node key-value store on jmsadair/raft v0.2.0 with the library's own `Log`, `StateStorage` and `SnapshotStorage`, a simulated disk, and 4 clients checked for linearizability.

```
cd examples/jmsraft
seedfall replay -c seedfall.json -seed 4 -faults statefile.faults.json
```

One fault is enough:

```
    1.413s  fault crash n3#1 during disk op 309, restart at 2.006s
    2.006s  start n3#2
    2.006s  FAIL  n3#2: node restarts from its own disk: could not recover state from storage: could not decode state: could not read length of protobuf message: EOF
```

A crash at an arbitrary moment, not inside a disk operation, is also enough (seedfall's crash drops unsynced data, like a power loss):

```
seedfall replay -c seedfall.json -seed 16 -faults statefile.crash.faults.json
    1.409s  fault crash n1#1, restart at 1.585s
    1.585s  FAIL  n1#2: node restarts from its own disk: could not recover state from storage: could not decode state: could not read protobuf message: unexpected EOF
```

In a 150-seed sweep, 125 seeds fail with a node that cannot restart; among seeds 1-60, 34 of the 50 failures are this bug.

Outside the simulator, an empty `state.bin` reproduces the restart failure on a real filesystem:

```go
os.WriteFile(filepath.Join(dir, "state", "state.bin"), nil, 0o644)
s, _ := raft.NewStateStorage(dir)
_, _, err := s.State() // could not decode state: could not read length of protobuf message: EOF
```

## Suggested fix

Sync and close the temporary file before the rename:

```go
if err := encodePersistentState(tmpFile, p.state); err != nil {
	return fmt.Errorf("could not encode state: %w", err)
}
if err := tmpFile.Sync(); err != nil {
	return fmt.Errorf("could not sync temporary file: %w", err)
}
if err := tmpFile.Close(); err != nil {
	return fmt.Errorf("could not close temporary file: %w", err)
}
```

For strict POSIX durability the state directory should also be fsynced after the rename. The library never fsyncs a directory (see the strict-POSIX note in [jmsadair-raft-log-torn-tail.md](jmsadair-raft-log-torn-tail.md)).

**Fix verified:** with this change applied to a local copy of v0.2.0 (together with the two other restart fixes, [torn log tail](jmsadair-raft-log-torn-tail.md) and [temporary snapshot directory](jmsadair-raft-tmp-snapshot-dir.md)), `seedfall.json` passes 150 seeds: crashes, partitions, torn writes and crashes during disk I/O, with every history linearizable and the cluster accepting writes again after the faults stop.
