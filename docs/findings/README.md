# Findings

Bugs that seedfall found in open-source Go projects. Each report has the root cause with file and line references, a deterministic replay command, and a patched copy that was verified to pass. These are drafts and have not yet been filed upstream.

| Project | Finding | Affects | Minimal schedule | Fix verified |
|---|---|---|---|---|
| [rosedblabs/wal](rosedblabs-wal-torn-tail.md) v1.3.8 | After a crash mid-write, `Open` appends after the torn chunk. Later fsynced, acknowledged writes are unreachable to replay, and rosedb v2 fails to open | Any filesystem | 2 crashes | 1,000 seeds |
| [tidwall/buntdb](tidwall-buntdb-shrink-fsync.md) v1.3.2 | The automatic AOF shrink renames the rewritten file into place without fsyncing it or its directory. A crash after a shrink can bring the database back empty or partial, even with `SyncPolicy: Always` | Filesystems without rename-over-write heuristics | 4 crashes, 1 partition | 300 seeds |
| [tidwall/wal](tidwall-wal-torn-tail.md) v1.2.1 | A crash mid-write makes `Open` fail with `log corrupt` forever (known: #21). The pending fix, PR #22, skips the torn entry but never truncates it, so the segment corrupts again later | Any filesystem | 1 crash | 1,500 seeds |
| [hashicorp/raft](hashicorp-raft-snapshot-dir-fsync.md) v1.8 | `FileSnapshotSink` never fsyncs the snapshot directory before renaming it. Under strict POSIX, one crash leaves an empty snapshot after the log was compacted, and the node panics on every restart | Strict POSIX filesystems | 1 crash | 120 seeds |

Systems that passed (no false alarms):

| Project | Workload | Result |
|---|---|---|
| hashicorp/raft v1.8 | 3 nodes, 4 concurrent clients, Porcupine | 600 seeds durable, 60 seeds linearizable, also over the stock `NewTCPTransport` |
| etcd-io/raft v3.7 | 3 nodes, raftexample-style storage, Porcupine | 100 seeds, plus 100 under strict directories |
| tidwall/buntdb v1.3.2 | `SyncPolicy: Always` without shrink | 300 seeds, plus 300 with torn writes and crashes mid-I/O |

Each system also had a negative control (for example, skipping fsync) that seedfall caught, so the passing results are not vacuous.
