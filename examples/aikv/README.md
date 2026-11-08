# Can a model write a crash-safe key-value store?

We asked Claude for an embedded, persistent key-value store nine times: three samples each from the `haiku`, `sonnet` and `opus` models in Claude Code (Haiku 5.5, Sonnet 5.5 and Opus 5.5 on 2026-11-08). Every sample got the same prompt ([PROMPT.md](PROMPT.md)) in a fresh session, with no other context. It asks for durability across crashes and power loss, recovery from partial writes, crash-safe compaction, and production quality.

All nine wrote tests, and all nine test suites passed. All nine chose the textbook design: a CRC-checked append-only log, an fsync before every acknowledgement, and compaction that writes a new file and switches over atomically. Then we ran them under seedfall.

## Result

Seven of the nine survived everything. Two have a bug that makes the store refuse to open forever after a specific crash, losing every acknowledged write. Both bugs are on recovery paths their own tests could not reach.

| Sample | Lines | base | strict | eio | harsh | reorder | fsync_noop | dirsync_noop |
|---|---:|---|---|---|---|---|---|---|
| haiku-1 | 531 | 20 ok | 20 ok¹ | 20 ok | 100 ok | **FAIL seed 1** | killed | killed |
| haiku-2 | 571 | 20 ok | 20 ok¹ | 20 ok | 100 ok | 100 ok | killed | killed |
| haiku-3 | 476 | 20 ok | 20 ok | 20 ok | 100 ok | 100 ok | killed | killed |
| sonnet-1 | 979 | 20 ok | 20 ok | 20 ok | 100 ok | 100 ok | killed | killed |
| sonnet-2 | 928 | 20 ok | 20 ok | 20 ok | 100 ok | 100 ok | killed | survived² |
| sonnet-3 | 1027 | 20 ok | 20 ok | 20 ok | 100 ok | 100 ok | killed | killed |
| opus-1 | 600 | 20 ok | 20 ok | 20 ok | 100 ok | 100 ok | killed | killed |
| opus-2 | 1003 | 20 ok | 20 ok | 20 ok | **FAIL seed 99** | 100 ok | killed | killed |
| opus-3 | 735 | 20 ok | 20 ok | 20 ok | 100 ok | 100 ok | killed | killed |

¹ After a two-line port, below. ² Explained below; a gap in this harness, not in the store.

The fault profiles, all with crashes at 0.5 per second, torn writes, and crashes in the middle of 0.5% of disk operations:

| Profile | Adds |
|---|---|
| base | nothing |
| strict | `strict_dirs`: a new or renamed file survives a crash only after its directory is fsynced |
| eio | `fsync_eio`: 1% of fsyncs fail with `EIO`, and the unsynced data stays in the page cache, as on Linux |
| harsh | `strict_dirs`, `fsync_eio`, and crashes in 2% of disk operations |
| reorder | `strict_dirs` and `reorder_writes`: unsynced writes reach the disk page by page, in any order |

`fsync_noop` and `dirsync_noop` are [mutants](../../docs/mutate.md): seedfall turns every file fsync, or every directory fsync, into a no-op, and checks that some seed now fails. A killed mutant shows the tests would notice that durability mistake.

## Finding 1: opus-2 corrupts itself after a failed fsync during recovery

Seed 99 of `harsh`, shrunk to 7 faults.

A crash tears the last segment so badly that less than its 8-byte header survives. On the next open, recovery rewrites the header in place and fsyncs it, and that fsync fails with `EIO`. The store gives up, but the new header is still in the page cache, so the next process sees a valid header, appends records and fsyncs them successfully. The header was never written back. After the next crash the segment starts with 8 zero bytes, and every later open fails with `data corruption detected: invalid header in seg-00000000000000000001.log`. The data is fine; the store just refuses to read it.

This is the fsyncgate pattern: after a failed fsync, the kernel can mark dirty pages clean, so data that a later read returns may never reach the disk. The fix ([fixes/opus-2-header.diff](fixes/opus-2-header.diff)) recreates a too-short segment by writing the header to a temporary file, fsyncing it, and renaming it into place, so a failed fsync can't leave a header that only exists in memory. With the fix, seed 99 and 100 `harsh` seeds pass.

```
seedfall run -c configs/opus-2.harsh.json -seeds 99
```

## Finding 2: haiku-1 refuses to open after out-of-order writeback

Seed 1 of `reorder`, shrunk to 3 crashes.

Recovery reads records until the first one that fails its checksum. Everything after that must be zero bytes, or the store declares `log is corrupt` and refuses to open. That check assumes a crash leaves a clean prefix of the unsynced tail. Page writeback does not keep that promise: of the last few unsynced records, a later page can reach the disk while an earlier one is still zeros. The tail then reads as a hole followed by data. The store calls that corruption, and it can never open again, although every acknowledged write is intact before the hole.

Which file systems do this? ext4 in its default `data=ordered` mode writes appended data before it extends the file size, so it should not expose the hole. XFS updates the size as each write completes, and ext4 with `data=writeback` makes no ordering promise, so both can. seedfall's [limitations](../../README.md#limitations) note that its page model is weaker than ext4's default.

The fix ([fixes/haiku-1-tail.diff](fixes/haiku-1-tail.diff)) treats everything after the first invalid record as an unfinished tail and truncates it. Without a record of where the last fsync ended, a store can't tell a torn tail from corruption in the middle of the log, so this trades corruption detection for availability. With the fix, seed 1 and 100 `reorder` seeds pass.

```
seedfall run -c configs/haiku-1.reorder.json -seeds 1
```

## The port: two samples skip directory fsyncs on wasip1

haiku-1 and haiku-2 put `wasip1` with Windows in the list of platforms where they skip fsyncing a directory. As written, both fail `strict` (seed 1 and seed 9: an acknowledged write is lost after a crash, because a new file's directory entry never became durable). That outcome only says the wasip1 build is unsafe, so we changed two lines to make the wasip1 build behave like the Linux build, and the table above tests that.

- haiku-1: `syncdir_unix.go` builds for `unix || wasip1`, and `syncdir_other.go` for `!unix && !wasip1`.
- haiku-2: `wasip1` is removed from the `case "windows", "wasip1", "js":` that skips the directory fsync.

## Why sonnet-2 survived dirsync_noop

sonnet-2 starts a new segment at 64 MiB, more than this workload writes in total (5,000 writes of 8 KiB), so it creates very few files, and most of the weakened directory fsyncs guard files that never matter at the end. The client only checks each key's final value. Losing an early segment is invisible once every key has been overwritten in a later one. A workload that reads keys back during the run, or a smaller segment size, would close this gap. The other eight stores rename a temporary file over their data during compaction, which `dirsync_noop` breaks for good.

## The harness

- [`server`](server) serves `PUT`, `GET` and `DEL` over TCP for whichever store `-impl` names. It exits if `Open` fails, and seedfall's supervisor restarts it, as systemd would.
- [`client`](client) sends 5,000 operations over 20 keys with 8 KiB values, 20% of them deletes, and records which writes were acknowledged and which are ambiguous because the connection broke. At the end it reads every key and fails if the store returns anything other than the last acknowledged value or a later ambiguous one. It also fails if the store refuses to serve for 10 seconds in a row: a store that never opens again has lost its data too.
- [`impl`](impl) holds each sample's non-test files, unchanged apart from the port above.
- [`configs`](configs) holds one config per sample and profile.

Reproduce any cell of the table:

```
cd examples/aikv/configs
seedfall run -c sonnet-3.reorder.json -seeds 1-100
seedfall mutate -c sonnet-3.strict.json -seeds 1-20
```
