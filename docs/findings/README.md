# Findings

Bugs that seedfall found in open-source Go projects. Each report has the root cause with file and line references, a deterministic replay command, and a patched copy that was verified to pass. Each one is reported upstream; the link is at the top of each report.

| Project | Finding | Affects | Minimal schedule | Fix verified |
|---|---|---|---|---|
| [nats-io/nats-server](nats-stream-meta-checksum-crash.md) v2.15.2 | A crash during stream creation leaves `meta.inf` without `meta.sum`. On restart the stream is skipped; the client re-creates it, but the checksum is never rewritten, so every message acknowledged afterwards vanishes on the next restart. Also, under strict POSIX, new stream directories and message blocks are never directory-fsynced | Any filesystem | 1 crash mid-I/O, 1 crash | 150 seeds |
| [rosedblabs/wal](rosedblabs-wal-torn-tail.md) v1.3.8 | After a crash mid-write, `Open` appends after the torn chunk. Later fsynced, acknowledged writes are unreachable to replay, and rosedb v2 fails to open | Any filesystem | 2 crashes | 1,000 seeds |
| [tidwall/buntdb](tidwall-buntdb-shrink-fsync.md) v1.3.2 | The automatic AOF shrink renames the rewritten file into place without fsyncing it or its directory. A crash after a shrink can bring the database back empty or partial, even with `SyncPolicy: Always` | Filesystems without rename-over-write heuristics | 4 crashes, 1 partition | 300 seeds |
| [tidwall/wal](tidwall-wal-torn-tail.md) v1.2.1 | A crash mid-write makes `Open` fail with `log corrupt` forever (known: #21). The pending fix, PR #22, skips the torn entry but never truncates it, so the segment corrupts again later | Any filesystem | 1 crash | 1,500 seeds |
| [hashicorp/raft](hashicorp-raft-snapshot-dir-fsync.md) v1.8 | `FileSnapshotSink` never fsyncs the snapshot directory before renaming it. Under strict POSIX, one crash leaves an empty snapshot after the log was compacted, and the node panics on every restart | Strict POSIX filesystems | 1 crash | 120 seeds |
| [nutsdb/nutsdb](nutsdb-bucket-meta-fsync.md) v1.1.0 | `bucket.Meta` is written but never fsynced. After a crash, recovery drops every entry in a bucket whose record was lost, even though each commit was fsynced and acknowledged | Any filesystem | 1 crash | 300 seeds |
| [nutsdb/nutsdb](nutsdb-torn-tail-crc.md) v1.1.0 | A torn write at the end of the active data file is reported as `crc error`, and `Open` fails on every restart | Any filesystem | 1 crash mid-I/O | 300 seeds |
| [nutsdb/nutsdb](nutsdb-rotation-dir-fsync-and-eio.md) v1.1.0 | A new data file is never directory-fsynced, and a failed fsync doesn't rewind the write offset, so acknowledged writes can be lost | Strict POSIX; fsync errors | 1 crash; 1 EIO, 1 crash | 300 seeds |
| [prologic/bitcask](bitcask-config-json-torn.md) v2.2.0 | Every `Open` rewrites `config.json` in place without fsync. A crash can leave it torn, and every later `Open` fails | Any filesystem | 1 crash mid-I/O | 300 seeds |
| [prologic/bitcask](bitcask-autorecovery-torn-tail.md) v2.2.0 | Auto-recovery fails on the ordinary torn tail it exists for, so `Open` errors out after a crash mid-`Put` | Any filesystem | 1 crash mid-I/O | 300 seeds |
| [prologic/bitcask](bitcask-recovered-datafile-fsync.md) v2.2.0 | Auto-recovery renames an unsynced copy over the last datafile, so a second crash can wipe acknowledged writes | Any filesystem | 3 faults | 300 seeds |
| [prologic/bitcask](bitcask-rotation-dir-fsync-and-eio.md) v2.2.0 | A new datafile is never directory-fsynced, and a failed fsync doesn't rewind the offset | Strict POSIX; fsync errors | 1 crash; 1 EIO, 1 crash | 300 seeds |
| [peterbourgon/diskv](diskv-rename-dir-fsync.md) v3.0.1 | `WriteStream` with sync and `TempDir` never fsyncs the directory after the rename, so an acknowledged write can revert | Strict POSIX | 3 faults | 300 seeds |
| [jmsadair/raft](jmsadair-raft-state-file-fsync.md) v0.2.0 | `SetState` renames the term and vote file into place without fsyncing it. One crash leaves `state.bin` empty, and the node fails to start forever | Any filesystem | 1 crash | 150 seeds (all four fixes) |
| [jmsadair/raft](jmsadair-raft-log-torn-tail.md) v0.2.0 | `Replay` never truncates a torn log tail. A partial entry fails every restart; an orphan length prefix is read as a clean end, so new acknowledged entries land behind it and the next restart fails | Any filesystem | 1 crash | 150 seeds |
| [jmsadair/raft](jmsadair-raft-tmp-snapshot-dir.md) v0.2.0 | A crash mid-snapshot leaves a temp directory whose cleanup fails the next start | Any filesystem | 1 crash | 150 seeds |
| [jmsadair/raft](jmsadair-raft-snapshot-wall-clock-order.md) v0.2.0 | Snapshots are ordered by wall-clock name, so after a clock step back a restart loads an old snapshot behind the compacted log and never starts | Any filesystem | 1 clock jump, 2 crashes | 100 seeds |

Systems that passed (no false alarms):

| Project | Workload | Result |
|---|---|---|
| hashicorp/raft v1.8 | 3 nodes, 4 concurrent clients, Porcupine | 600 seeds durable, 60 seeds linearizable, also over the stock `NewTCPTransport` |
| etcd-io/raft v3.7 | 3 nodes, raftexample-style storage, Porcupine | 100 seeds, plus 100 under strict directories |
| NATS JetStream v2.15.2 | `SyncAlways`, R1 file stream, 300 publishes | 150 seeds under crashes, torn writes and crashes mid-I/O |
| NATS JetStream cluster v2.15.2 | 3 nodes, R3 stream on NATS raft, `sync_always`, faults during publishes | 200 seeds; under strict directories it loses acknowledged R3 messages after three single-node crashes, the same missing directory fsyncs as the single-server finding |
| tidwall/buntdb v1.3.2 | `SyncPolicy: Always` without shrink | 300 seeds, plus 300 with torn writes and crashes mid-I/O |

Each system also had a negative control (for example, skipping fsync) that seedfall caught, so the passing results are not vacuous.
