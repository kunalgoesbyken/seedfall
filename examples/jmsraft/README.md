# jmsraft

A 3-node key-value store on [jmsadair/raft](https://pkg.go.dev/github.com/jmsadair/raft) v0.2.0, a standalone Raft library with PreVote, linearizable and lease-based reads, automatic snapshots and its own file-based log, term/vote and snapshot storage. The nodes use the library's storage unchanged and snapshot every 32 entries. Writes go through `SubmitOperation` as replicated operations, reads as `LinearizableReadOnly` operations. 4 concurrent clients are checked for linearizability; then the faults stop, and the cluster must accept a write again within 20 seconds. A node that cannot start from its own disk fails the run (an `EIO` at startup is treated as a normal fail-stop exit instead, and the node is restarted).

The network runs through `net_overlay`, and the library's stock gRPC transport works there unchanged: `grpc.json` runs it under crashes and partitions. After a peer crashes, gRPC's transparent retry busy-waits without a syscall until another goroutine publishes the connection-state change; seedfall's [deterministic preemption](../../docs/how-it-works.md#preemption) lets that goroutine run, as Go's async preemption does on real hardware. The other configs, and the findings below, plug in a plain TCP transport through `raft.WithTransport` (`node/transport.go`), reusing the stock transport's configuration encoding. `-transport grpc` switches back to the stock one.

| Config | Result |
|---|---|
| `seedfall.json` | crashes, partitions, torn writes, crashes during disk I/O: fails on seed 4. 125 of 150 seeds fail, every one with a node that cannot restart from its own disk: three bugs, below. With the three fixed, 150 seeds pass, linearizable |
| `seedfall.strict.json` | adds strict POSIX directories: fails on seed 2. The library never fsyncs a directory. With directory fsyncs added to the three fixes, 150 seeds pass |
| `seedfall.eio.json` | adds fsync `EIO`: fails on seed 5. The library exits on a failed fsync, but the restarted process trusts the page cache (fsyncgate), and a later crash leaves a zero-filled hole that replays as empty entries, without a checksum to catch it |
| `clock.json` | crashes and wall-clock jumps, no disk faults: fails on seed 13, a node that loads an old snapshot after its clock stepped back. With the four fixes, 100 seeds pass |
| `pause.json` | process pauses and wall-clock jumps, linearizable reads: passes (100 seeds tested) |
| `pause.lease.json` | the same with lease-based reads: fails on seed 11 with the snapshot ordering bug; nothing else fails. With the four fixes, 100 seeds pass, linearizable |
| `grpc.json` | the stock gRPC transport, crashes and partitions, no disk faults: passes (40 seeds tested). Without preemption, seed 8 reported a false watchdog livelock after two crashes |
| `localreads.json` | `-local-reads` serves reads from the local state machine, bypassing raft: fails on seed 1, not linearizable. The negative control |

## Findings

The first three take one fault each.

- [The term and vote file is renamed into place without an fsync](../../docs/findings/jmsadair-raft-state-file-fsync.md): one crash, and the node never starts again.
  ```sh
  seedfall replay -c seedfall.json -seed 4 -faults statefile.faults.json
  seedfall replay -c seedfall.json -seed 16 -faults statefile.crash.faults.json
  ```
- [A torn log tail is never truncated](../../docs/findings/jmsadair-raft-log-torn-tail.md): a partial record fails every restart; a record cut right after its length prefix is accepted, new entries are appended behind it, and the next restart fails.
  ```sh
  seedfall replay -c seedfall.json -seed 7 -faults tornlog.faults.json
  ```
- [A crash mid-snapshot breaks the next start](../../docs/findings/jmsadair-raft-tmp-snapshot-dir.md): `RemoveTmpFiles` deletes the leftover temporary directory, then fails walking into it.
  ```sh
  seedfall replay -c seedfall.json -seed 16 -faults tmpdir.faults.json
  ```
- [Snapshots are ordered by wall-clock time](../../docs/findings/jmsadair-raft-snapshot-wall-clock-order.md): after the clock steps back, a restart loads an old snapshot behind the compacted log, and the node never starts again.
  ```sh
  seedfall replay -c clock.json -seed 13 -faults snapshotorder.faults.json
  ```

The upstream GitHub repository is no longer public; v0.2.0 is still served by the Go module proxy.
