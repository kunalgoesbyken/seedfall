# Inspecting the disk at the moment of failure

`seedfall replay` tells you what happened. `seedfall inspect` shows you what is on disk when it goes wrong: every file on every node, how much of it is durable, which bytes a crash would lose, which directory entries are not durable yet, and where the last crash tore a write.

```sh
seedfall inspect -c rosedb.json -seed 1
```

```
seed 1 failed at t=3.12130224s: client#1: acknowledged entry 27 lost: server now answers "VAL e141-1111111111111111111111"
disk state at t=3.12130224s, the moment of failure

node server (directory entries are durable once made; torn_writes: a crash may also keep a prefix of the pending writes)
  path                                                   size    durable  state
  data/                                                     -          -  durable
    wal/                                                    -          -  durable
      000000001.SEG                                      4029       4029  durable
        ^ torn by the crash at t=427.268199ms: kept 60 of a 73-byte write at offset 2019
      000000002.SEG                                      3979       3979  durable
      000000003.SEG                                      2054       2054  durable
  2 dirs, 3 files: 10062 bytes durable, 0 bytes pending, 0 entries lost on crash, 0 removed entries that come back

node client (directory entries are durable once made; torn_writes: a crash may also keep a prefix of the pending writes)
  empty disk
```

The crash at 427ms left a 60-byte fragment of entry 26 at offset 2019, and every later entry was appended after it. That is the rosedblabs/wal bug in [findings](findings/rosedblabs-wal-torn-tail.md), visible without reading any code.

## Usage

```sh
seedfall inspect [-c seedfall.json] -seed N [-faults file.json] [-at DURATION] [-node name] [-cat path | -out dir]
```

| Flag | Meaning |
|---|---|
| `-seed` | Seed to replay |
| `-faults` | Replay with a fault schedule from `seedfall shrink` |
| `-at` | Snapshot at this virtual time instead of at the failure, for example `-at 4.4s`. The snapshot is taken at the last scheduler step at or before it. If the run fails or ends first, the snapshot is taken there. |
| `-node` | Only this node |
| `-cat` | Print the durable content of one file: text as is, binary as a hex dump. The file must exist in the durable image. |
| `-out` | Write each node's durable disk image to `dir/<node>/`, so you can open it with the library's own tools. The target must not exist yet. |

The run is the same as `seedfall replay`: same seed, same faults, same trace. Inspecting changes nothing about it.

## Reading the output

Each file has its live size (what the program reads now) and its durable size (what survives a crash), and one state:

| State | Meaning |
|---|---|
| `durable` | The live content and the durable content are the same, and the name survives a crash |
| `PENDING: N bytes lost on crash at [a,b) ...` | These byte ranges of the live content differ from the durable content: they were written but not fsynced |
| `PENDING truncate` | The file was truncated but not fsynced; a crash restores the old bytes |
| `NOT DURABLE: never fsynced` | Without `strict_dirs`, a file that was never fsynced disappears on crash |
| `NOT DURABLE: entry vanishes on crash` | With `strict_dirs`, the directory entry is not durable until the parent directory is fsynced, even if the file's data is |

A `^ torn by the crash at t=...` line under a file says the last crash kept only part of one write: how many bytes of it, and at which offset. Those partial bytes are now durable and are what recovery code has to cope with.

With `strict_dirs`, a file that was unlinked or renamed away while its parent directory was not fsynced is listed separately as an entry that a crash brings back. Renaming a temporary file over an existing one without fsyncing the directory shows up as the new name `NOT DURABLE` and the old content coming back.

The durable image is the disk a crash would leave without `torn_writes`. With `torn_writes`, a crash can also keep an in-order prefix of the pending writes and, with `strict_dirs`, of the pending directory operations.

## Examples

NATS JetStream, the stream that vanishes ([finding](findings/nats-stream-meta-checksum-crash.md)):

```sh
cd examples/natsjs
seedfall inspect -c seedfall.always.json -seed 40 -faults metasum.faults.json
```

```
          EVENTS/                                           -          -  durable
            meta.inf                                      517        517  durable
            meta.sum.tmp                                    6          6  durable
              ^ torn by the crash at t=158.20724ms: kept 6 of a 16-byte write at offset 0
            msgs/                                           -          -  durable
              1.blk                                      7702       7702  durable
```

`meta.inf` exists, `meta.sum` does not, and only a torn `meta.sum.tmp` is left. NATS skips a stream without a checksum on restart, and because `meta.inf` exists it never writes `meta.sum` again, so the 1.blk messages acknowledged since are skipped on every later restart.

tidwall/buntdb, one step before the crash that empties the database ([finding](findings/tidwall-buntdb-shrink-fsync.md)):

```sh
cd examples/buntdb
seedfall inspect -c seedfall.shrink.json -seed 27 -at 4.417s -node server
```

```
  data/                                                     -          -  durable
    db                                                   1255          0  NOT DURABLE: never fsynced, vanishes on crash
```

The AOF shrink renamed its rewritten file into place without fsyncing it: 1255 bytes live, none durable. At the failure, `db` holds only the 211 bytes that the crash at 4.417s happened to keep.

## From an agent

The MCP server exposes the same snapshot as `seedfall_inspect`, with the per-file sizes, volatile byte ranges, entry durability and last tear as JSON, and optionally one file's durable content. See [agents.md](agents.md).

## Limitations

- The snapshot covers the simulated disk only, not process memory or in-flight network messages.
- The last tear is recorded per file. A later write over the same range does not clear it.
- `-cat` prints the whole file. Pipe it through `tail` for large files.
