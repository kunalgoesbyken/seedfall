# raftkv

A 3-node key-value store on [hashicorp/raft](https://github.com/hashicorp/raft) v1.8 with a file-backed log store and `FileSnapshotStore`. `client` writes through the leader and verifies every acknowledged key; `linclient` runs 4 concurrent clients and records a history for the linearizability check.

| Config | Result |
|---|---|
| `seedfall.json` | passes (600 seeds tested) under crashes, crashes mid-I/O, torn writes and partitions |
| `seedfall.nofsync.json` | log store skips fsync: fails on seed 9, a committed write lost. Shrinks from 75 faults to 3 |
| `seedfall.lin.json` | passes the linearizability check (60 seeds tested) |
| `nofsync.lin.json` | fails on seed 26: history not linearizable |
| `strict.lin.json` | strict POSIX directories: fails on seed 7, the [snapshot directory finding](../../docs/findings/hashicorp-raft-snapshot-dir-fsync.md) |
| `tcp.lin.json` | stock `raft.NewTCPTransport` and plain `net.Listen` via `net_overlay`: passes |
| `tcp.nofsync.lin.json` | the same with fsync skipped: fails (seed 92) |
| `pause.lin.json` | process pauses and wall-clock jumps only: passes (150 seeds tested). Reads call `VerifyLeader` first |
| `pause.localreads.lin.json` | the same, but `-local-reads` serves reads without confirming leadership: fails on seed 35, history not linearizable. A paused old leader wakes up and answers a read before it notices a new leader took over |

The node binary takes `-tcp` to switch from the `sim` stream layer to the stock TCP transport, `-no-fsync` and `-local-reads` for negative controls. The `client` workload also checks liveness: after its writes it calls `sim.QuietFor(10s)` and requires the cluster to serve reads again within that window.
