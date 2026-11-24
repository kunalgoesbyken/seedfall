# Findings

Bugs that seedfall found in open-source Go projects. Every one replays exactly from its seed, has a full report with the root cause and file and line references, and has a patched copy that was verified to pass. Upstream status is as of 2026-11-24.

Each finding has a plain-English summary of what goes wrong and the technical cause for the people who maintain the code. Click a project for the full report.

## How to read this

- **Confirmed write**: data the program said was saved. Losing one is the worst kind of storage bug, because the user was told it was safe.
- **Crash mid-write**: the power goes out or the process dies while data is being written, so only part of it reaches the disk.
- **fsync**: the call that forces data out of memory onto the disk. Anything not fsynced can vanish in a crash.
- **Strict POSIX filesystem**: a filesystem that keeps a newly created or renamed file through a crash only if its folder was fsynced too, as the POSIX standard allows. ext4 and XFS are usually more forgiving, so some of these bugs are rarer there, but code that depends on that is still unsafe.
- **Shortest failure**: what `seedfall shrink` cut the failing run down to, the fewest faults that still break it.

## Data it confirmed is lost

| Project | What goes wrong | Technical cause | Upstream |
|---|---|---|---|
| [nats-io/nats-server](nats-stream-meta-checksum-crash.md) v2.15.2 | A crash while a stream is created makes it forget every message it confirms from then on, at the next restart. | A crash during stream creation leaves `meta.inf` without `meta.sum`. On restart the stream is skipped; the client re-creates it, but the checksum is never rewritten. Under strict POSIX, new stream directories and message blocks are also never directory-fsynced.<br>*Any filesystem · 1 crash mid-I/O, 1 crash · fix passes 150 seeds* | [nats-server#8872](https://github.com/nats-io/nats-server/issues/8872), fixed in [#8887](https://github.com/nats-io/nats-server/pull/8887) |
| [rosedblabs/wal](rosedblabs-wal-torn-tail.md) v1.3.8 | After one crash mid-write, new writes land behind the damaged data and can never be read back. rosedb, which is built on it, stops opening. | `Open` appends after the torn chunk, so later fsynced, acknowledged writes are unreachable to replay.<br>*Any filesystem · 2 crashes · fix passes 1,000 seeds* | [wal#51](https://github.com/rosedblabs/wal/issues/51), open |
| [tidwall/buntdb](tidwall-buntdb-shrink-fsync.md) v1.3.2 | When it compacts its file, it never makes sure the new file is on disk. A crash afterwards can bring the database back empty or partial. | The automatic AOF shrink renames the rewritten file into place without fsyncing it or its directory, even with `SyncPolicy: Always`.<br>*Filesystems without rename-over-write heuristics · 4 crashes, 1 partition · fix passes 300 seeds* | [buntdb#126](https://github.com/tidwall/buntdb/issues/126), fixed in [6a23821](https://github.com/tidwall/buntdb/commit/6a23821fdfd51ab6e375b8f82407b6589cad907f) |
| [nutsdb/nutsdb](nutsdb-bucket-meta-fsync.md) v1.1.0 | One crash can make a whole bucket's data disappear, even though every write to it was confirmed. | `bucket.Meta` is written but never fsynced, so recovery drops every entry in a bucket whose record was lost.<br>*Any filesystem · 1 crash · fix passes 300 seeds* | [nutsdb#695](https://github.com/nutsdb/nutsdb/issues/695), open |
| [nutsdb/nutsdb](nutsdb-rotation-dir-fsync-and-eio.md) v1.1.0 | Confirmed writes can be lost when it starts a new data file, or when the disk reports an error while saving. | A new data file is never directory-fsynced, and a failed fsync doesn't rewind the write offset.<br>*Strict POSIX; fsync errors · 1 crash; 1 EIO, 1 crash · fix passes 300 seeds* | [nutsdb#697](https://github.com/nutsdb/nutsdb/issues/697), open |
| [prologic/bitcask](bitcask-recovered-datafile-fsync.md) v2.2.0 | Its crash recovery puts back a data file that isn't safely on disk yet, so a second crash can wipe confirmed writes. | Auto-recovery renames an unsynced copy over the last datafile.<br>*Any filesystem · 3 faults · fix passes 300 seeds* | no link recorded |
| [prologic/bitcask](bitcask-rotation-dir-fsync-and-eio.md) v2.2.0 | Confirmed writes can be lost when it starts a new data file, or when the disk reports an error while saving. | A new datafile is never directory-fsynced, and a failed fsync doesn't rewind the offset.<br>*Strict POSIX; fsync errors · 1 crash; 1 EIO, 1 crash · fix passes 300 seeds* | no link recorded |
| [peterbourgon/diskv](diskv-rename-dir-fsync.md) v3.0.1 | A crash can undo a write it already confirmed. | `WriteStream` with sync and `TempDir` never fsyncs the directory after the rename.<br>*Strict POSIX · 3 faults · fix passes 300 seeds* | [diskv#83](https://github.com/peterbourgon/diskv/issues/83), open |
| [etcd-io/bbolt](bbolt-create-dir-fsync.md) v1.5.0 | A crash soon after a new database is created can lose the whole file, with every transaction it committed. | `Open` creates the database file and syncs its data but never fsyncs the directory.<br>*Strict POSIX · 1 crash · fix passes 300 seeds* | not filed yet |
| [jmsadair/raft](jmsadair-raft-log-torn-tail.md) v0.2.0 | Damage from a crash mid-write is never cleaned up, so the node either fails every restart or quietly loses entries written after it. | `Replay` never truncates a torn log tail. A partial entry fails every restart; an orphan length prefix is read as a clean end, so new acknowledged entries land behind it.<br>*Any filesystem · 1 crash · fix passes 150 seeds* | not filed: repository no longer public |

## It won't start again after a crash

| Project | What goes wrong | Technical cause | Upstream |
|---|---|---|---|
| [tidwall/wal](tidwall-wal-torn-tail.md) v1.2.1 | One crash during a write and the log can never be opened again. The pending upstream fix is incomplete. | `Open` fails with `log corrupt` forever (known: #21). PR #22 skips the torn entry but never truncates it, so the segment corrupts again later.<br>*Any filesystem · 1 crash · fix passes 1,500 seeds* | [wal#43](https://github.com/tidwall/wal/issues/43), open |
| [hashicorp/raft](hashicorp-raft-snapshot-dir-fsync.md) v1.8 | One crash while saving a snapshot leaves a node that crashes every time it starts. | `FileSnapshotSink` never fsyncs the snapshot directory before renaming it, so the snapshot is empty after the log was compacted.<br>*Strict POSIX · 1 crash · fix passes 120 seeds* | [raft#744](https://github.com/hashicorp/raft/issues/744), open |
| [nutsdb/nutsdb](nutsdb-torn-tail-crc.md) v1.1.0 | One crash during a write and the database refuses to open again. | A torn write at the end of the active data file is reported as `crc error`, and `Open` fails on every restart.<br>*Any filesystem · 1 crash mid-I/O · fix passes 300 seeds* | [nutsdb#696](https://github.com/nutsdb/nutsdb/issues/696), open |
| [prologic/bitcask](bitcask-config-json-torn.md) v2.2.0 | It rewrites its settings file every time it opens. A crash at that moment damages the file, and the database never opens again. | Every `Open` rewrites `config.json` in place without fsync, so it can be left torn.<br>*Any filesystem · 1 crash mid-I/O · fix passes 300 seeds* | no link recorded |
| [prologic/bitcask](bitcask-autorecovery-torn-tail.md) v2.2.0 | Its crash recovery fails on the most common kind of crash damage, so the database won't open after a crash during a write. | Auto-recovery fails on the ordinary torn tail it exists for, so `Open` errors out after a crash mid-`Put`.<br>*Any filesystem · 1 crash mid-I/O · fix passes 300 seeds* | no link recorded |
| [jmsadair/raft](jmsadair-raft-state-file-fsync.md) v0.2.0 | One crash can empty the file that records the node's vote, and the node never starts again. | `SetState` renames the term and vote file into place without fsyncing it, leaving `state.bin` empty.<br>*Any filesystem · 1 crash · fix passes 150 seeds (all four fixes)* | not filed: repository no longer public |
| [jmsadair/raft](jmsadair-raft-tmp-snapshot-dir.md) v0.2.0 | A crash while taking a snapshot leaves a leftover folder that stops the node from starting. | A crash mid-snapshot leaves a temp directory whose cleanup fails the next start.<br>*Any filesystem · 1 crash · fix passes 150 seeds* | not filed: repository no longer public |
| [jmsadair/raft](jmsadair-raft-snapshot-wall-clock-order.md) v0.2.0 | If the machine's clock moves backwards, the node later loads an old snapshot and never starts. | Snapshots are ordered by wall-clock name, so after a clock step back a restart loads an old snapshot behind the compacted log.<br>*Any filesystem · 1 clock jump, 2 crashes · fix passes 100 seeds* | not filed: repository no longer public |

## Systems that passed

seedfall found nothing wrong in these. To show that a pass means something, each was also run with a deliberately broken copy (for example, one that skips fsync), and seedfall caught every one.

| Project | What was tested | Result |
|---|---|---|
| hashicorp/raft v1.8 | 3 nodes, 4 concurrent clients, checked with Porcupine | 600 seeds durable, 60 seeds linearizable, also over the stock `NewTCPTransport` |
| etcd-io/raft v3.7 | 3 nodes, raftexample-style storage, checked with Porcupine | 100 seeds, plus 100 under strict directories |
| NATS JetStream v2.15.2 | `SyncAlways`, R1 file stream, 300 publishes | 150 seeds under crashes, torn writes and crashes mid-I/O |
| NATS JetStream cluster v2.15.2 | 3 nodes, R3 stream on NATS raft, `sync_always`, faults during publishes | 200 seeds. Under strict directories it loses acknowledged R3 messages after three single-node crashes, from the same missing directory fsyncs as the single-server finding |
| tidwall/buntdb v1.3.2 | `SyncPolicy: Always` without shrink | 300 seeds, plus 300 with torn writes and crashes mid-I/O |
