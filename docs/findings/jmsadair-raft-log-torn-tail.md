# A crash in the middle of a log append makes the node unable to restart, now or after its next restart

**Upstream:** not filed. The GitHub repository is no longer public, so there is nowhere to report it; v0.2.0 is still served by proxy.golang.org.

**Repo:** jmsadair/raft (tested on v0.2.0, the latest release, commit `8dd4cdc`; the GitHub repository is no longer public, the module is still served by proxy.golang.org)
**Severity:** one crash while an entry is being appended to `log/log.bin` leaves a node that fails `NewRaft` on every start until an operator repairs or deletes its log. The interrupted entry was never acknowledged, so nothing requires keeping it. No strict-POSIX assumptions needed: only that a crash can persist a prefix of the unsynced bytes. The second variant below does not even need a power loss: the length and the body are separate `write` calls, so a `kill -9` or OOM kill between them leaves exactly that tail.

## Summary

`encodeLogEntry` (`log.go:117-140`) writes each entry as two writes, a 4-byte big-endian length and then the protobuf body. `AppendEntries` (`log.go:274-295`) fsyncs after the batch. A crash before the fsync can persist any prefix of those bytes. `Replay` (`log.go:209-235`) handles neither kind of incomplete tail:

```go
for {
	entry, err := decodeLogEntry(reader)
	if errors.Is(err, io.EOF) {
		break
	}
	if err != nil {
		return fmt.Errorf("could not decode log entry: %w", err)
	}
	l.entries = append(l.entries, &entry)
}
```

1. **Partial length or partial body.** `binary.Read` or `io.ReadFull` returns `io.ErrUnexpectedEOF`, `Replay` returns an error, and `NewRaft` (`raft.go:301`, through `restore`, `raft.go:313`) fails on every start:

   ```
   could not replay log: could not decode log entry: could not read protobuf messsage: unexpected EOF
   ```

2. **Complete length, no body.** `io.ReadFull` reads zero bytes and returns plain `io.EOF`, which `Replay` treats as a clean end of the log. The node starts normally, but the 4 orphan bytes stay in the file, and the next append is written after them. From then on every new entry sits behind a record whose length prefix swallows the next entry's length prefix. Nothing goes wrong until the next restart, which fails on every attempt:

   ```
   could not replay log: could not decode log entry: could not unmarshal protobuf message: proto: cannot parse invalid wire-format data
   ```

   With seedfall's byte dump of the failing file: offset 182 holds `00000017` (an orphan length), immediately followed by the complete next record `00000017 0825 1001 18ba01 220c "PUT k3 c2-33" ...`.

Raft treats an incomplete trailing entry as never written: it was not fsynced, so it was never acknowledged to the leader and never counted toward a commit. Truncating it is always safe.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall): a 3-node key-value store on jmsadair/raft v0.2.0 with the library's own storage, a simulated disk with torn writes and crashes during disk I/O, and 4 clients checked for linearizability.

```
cd examples/jmsraft
seedfall replay -c seedfall.json -seed 7 -faults tornlog.faults.json
```

One fault is enough. The crash lands while n2 is appending an entry, and the node can never start again:

```
    1.049s  fault crash n2#1 during disk op 148, restart at 1.274s
    1.274s  start n2#2
    1.274s  FAIL  n2#2: node restarts from its own disk: could not replay log: could not decode log entry: could not read protobuf messsage: unexpected EOF
```

In a 150-seed sweep of this configuration, 125 seeds fail with a node that cannot restart; among seeds 1-60, 50 fail: 15 with this bug, 34 with the [state file bug](jmsadair-raft-state-file-fsync.md) and 1 with the [temporary snapshot directory bug](jmsadair-raft-tmp-snapshot-dir.md).

The second variant reproduces outside the simulator on a real filesystem, by appending only a length prefix to a valid log:

```go
l := open(dir)                       // raft.NewLog, Open, Replay
l.AppendEntries(entry(1, "PUT a 1"))
l.Close()
appendBytes(dir+"/log/log.bin", []byte{0, 0, 0, 23}) // crash kept the length, not the body

l = open(dir)                        // Replay: no error, LastIndex 1
l.AppendEntries(entry(2, "PUT b 2")) // fsynced and acknowledged
l.Close()

open(dir)                            // could not decode log entry: could not read protobuf messsage: unexpected EOF
```

## Suggested fix

Track the offset of each record during replay, and truncate the file at the start of an incomplete final record, whether the read stopped with `io.ErrUnexpectedEOF` or with `io.EOF` after part of the record:

```go
reader := &countingReader{r: bufio.NewReader(l.file)}
for {
	start := reader.n
	entry, err := decodeLogEntry(reader)
	if errors.Is(err, io.EOF) && reader.n == start {
		break
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		if err := l.truncateTornTail(start); err != nil { // Truncate, Sync, Seek(start)
			return err
		}
		break
	}
	if err != nil {
		return fmt.Errorf("could not decode log entry: %w", err)
	}
	l.entries = append(l.entries, &entry)
}
```

The `Seek` matters: after replay through a `bufio.Reader`, the file offset is at the old end of the file, and `AppendEntries` takes each entry's offset from `Seek(0, io.SeekCurrent)`. A per-record checksum would also catch tails that are not a clean prefix, but is not needed for the crash behavior above.

**Fix verified:** with this change applied to a local copy of v0.2.0 (together with the [state file](jmsadair-raft-state-file-fsync.md) and [temporary snapshot directory](jmsadair-raft-tmp-snapshot-dir.md) fixes), `seedfall.json` passes 150 seeds of crashes, partitions, torn writes and crashes during disk I/O, every history linearizable.

## Related, strict-POSIX only (lower priority)

The library never fsyncs a directory: not after `os.MkdirAll` of `log/`, `state/` and `snapshots/`, not after creating `log.bin`, and not after any of its renames (`state.bin`, log compaction at `log.go:435`, the snapshot directory at `snapshot_storage.go:135`). Under strict POSIX semantics a crash can roll back any of these. In the seedfall strict configuration this shows up as a node whose term, vote or log is rolled back, and in one seed as a leader that panics in `sendAppendEntries` (`raft.go:1007`, `make([]*LogEntry, 0, r.log.NextIndex()-nextIndex)` underflows because a follower reports a `nextIndex` past the leader's rolled-back log). With directory fsyncs added (after `MkdirAll`, after creating `log.bin`, after each rename, and of the temporary snapshot directory before its rename) on top of the three crash fixes, `seedfall.strict.json` passes 150 seeds.
