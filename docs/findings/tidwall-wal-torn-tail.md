# tidwall/wal can't reopen after a crash mid-append, and the pending fix (#22) leaves stale bytes that corrupt the log after the next segment rotation

**Upstream:** [tidwall/wal#43](https://github.com/tidwall/wal/issues/43), following up on #21 and PR #22

**Repo:** tidwall/wal (tested on v1.2.1, default options, `NoSync: false`)
**Status upstream:** the first half is known: issue #21, open since June 2023, and PR #22, unmerged. The second and third parts below are not reported.

## 1. A crash during one append bricks the log (known: #21)

With `NoSync: false`, every acknowledged write is fsynced, so a crash can only tear the entry that was being written and hadn't been acknowledged. On restart, `load()` parses the tail segment with `loadSegmentEntries` (wal.go:293). `loadNextBinaryEntry` (wal.go:599-606) returns `ErrCorrupt` for the partial entry, and `Open` fails. Every later `Open` fails the same way until someone edits the file by hand.

seedfall finds this on seed 1 and shrinks it to **one fault**:

```
cd examples/wals
seedfall replay -c tidwall.json -seed 1
  366ms  crash server during disk op #56
  server#2| open wal: log corrupt
  FAIL server#2 crashed
```

## 2. PR #22 recovers in memory but never truncates the file (new)

PR #22 adds `RecoverCorruptedTail`. When it's set, `loadSegmentEntries` stops at the first corrupt entry and trims `s.ebuf` to the valid prefix. `load()` then seeks the write offset back to `len(ebuf)`, but it **doesn't truncate the file**.

If the next append is shorter than the torn bytes, the end of the old partial entry stays in the file after the new entry. As long as the segment is the tail, recovery skips those bytes again. Once the segment rotates, it is loaded with `loadSegmentEntries(s, false)`, and `Read` of any index in that segment returns `ErrCorrupt`. One recoverable crash becomes permanent corruption in the middle of the log.

This was reproduced with PR #22 applied and `RecoverCorruptedTail: true`, on seed 117 (4 faults after shrinking). A diagnostic listing of segment `00000000000000000104` showed the following:

```
server#4 start: size=583 entries=6 valid=509 tail="me131-1111…"     (74-byte torn entry)
server#5 start: size=583 entries=7 valid=552 tail="1111111…"        (new 43-byte entry written at 509, 31 stale bytes left)
server#5| read wal: log corrupt
```

Fix: truncate the file to `len(lseg.ebuf)` and fsync it before seeking.

## 3. `cycle()` never fsyncs the directory (new; strict POSIX only)

`cycle()` (wal.go:355-376) creates the next segment with `O_CREATE|O_TRUNC`. It fsyncs the previous segment and, on later writes, the new one, but it never fsyncs the log directory. POSIX only makes the new file's name durable once the directory is fsynced.

Under seedfall's `strict_dirs` model, a crash after a rotation can lose the whole new segment, even though every entry in it was fsynced and acknowledged. Seed 2 shows this: after a single crash, segment `…041` is missing, and acknowledged entries 41 onward are gone. The server had already fsynced every ancestor directory at startup, so the only gap left is inside the library.

On ext4, XFS and btrfs, fsyncing the file usually persists its directory entry too, so typical Linux deployments are mostly not exposed. This is the same caveat as the hashicorp/raft snapshot finding.

## Verified fix

These are applied to a local copy in `.prof/tidwall-wal`; the full diff is in `.prof/tidwall-full-fix.diff`:

1. PR #22, with `RecoverCorruptedTail: true` in the example server
2. in `load()`: `l.sfile.Truncate(int64(len(lseg.ebuf)))` and `l.sfile.Sync()` before the `Seek`
3. in `cycle()`: fsync `l.path` after creating the new segment

| Variant | `tidwall.json` | `tidwall.strict.json` |
|---|---|---|
| v1.2.1 | FAIL seed 1 (1 fault) | FAIL |
| + PR #22 | FAIL seed 117 (stale tail, `ErrCorrupt` mid-log) | FAIL seed 2 (segment lost) |
| + PR #22 + truncate | ok, 1500 seeds | not run separately |
| + PR #22 + truncate + dir fsync | ok, 1500 seeds | ok, 1500 seeds |

The client also checks that every entry the server returns is a payload it actually sent, so a fix that skipped torn bytes but exposed them as phantom entries would fail too.

## Suggested upstream action

Comment on PR #22 with the truncate fix and the seed 117 reproduction. Then file a separate issue for the directory fsync, with the strict-POSIX caveat.
