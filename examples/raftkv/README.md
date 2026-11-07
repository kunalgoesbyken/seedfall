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

The node binary takes `-tcp` to switch from the `sim` stream layer to the stock TCP transport, and `-no-fsync` for the negative control.
