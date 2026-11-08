# How seedfall works

## One process, one clock, many nodes

seedfall compiles every node with the stock Go toolchain for `GOOS=wasip1` and runs each one as its own [wazero](https://wazero.io) module instance. On `wasip1` the Go runtime is single-threaded and has no async preemption, and every source of nondeterminism reaches the outside world through three WASI calls. seedfall implements all three in the host:

| WASI call | seedfall behavior |
|---|---|
| `random_get` | A ChaCha8 stream derived from the seed and the node's incarnation. This fixes map iteration order, `select` choice, `math/rand` and crypto randomness. |
| `clock_time_get` | A virtual clock shared by every node. With `clock_skew`, each node's wall clock is offset by a seeded amount; the monotonic clock is never skewed. |
| `poll_oneoff` | Parks the node until its earliest timer or its next network arrival. |

A node gives up control only when all of its goroutines are blocked. The scheduler runs exactly one node at a time, picks the next one by earliest wake-up time, breaks ties with the seed, and jumps virtual time forward. Ten seconds of `time.Sleep` cost no real time.

There is no `linkname`, no source rewriting and no forked runtime, so new Go releases keep working.

## Determinism

Scheduling, network latency, disk tearing, fault timing and I/O faults each draw from their own seeded stream. Removing one fault therefore barely disturbs the rest of the run, which is what makes shrinking work. The same seed produces the same trace byte for byte across processes and machines. `seedfall run -check` verifies this on every seed.

## Disk

Each node gets an in-memory file system mounted at `/` with an explicit durability model. Your code uses plain `os.File`.

- Writes land in the page cache immediately and are visible to reads.
- `Sync` makes the file's pending writes durable.
- A crash keeps only durable data. A file that was never synced disappears.
- With `torn_writes`, a crash also keeps a random in-order prefix of the pending writes, possibly ending in a partial write.
- With `reorder_writes`, a crash writes back dirty pages out of order, as kernel writeback does between fsyncs. Each pending write is split into 4 KiB pages. For every page the crash keeps a random in-order prefix of the writes to it, and pages are chosen independently, so a later write can be durable while an earlier write to another page is lost. Pending truncates are size changes and form their own in-order group. With `torn_writes` as well, the last write kept in each page can be partial. This is the page-cache behavior that ALICE ("All File Systems Are Not Created Equal", OSDI '14) found on ext4 `data=ordered` with delayed allocation: overwrites and appends can reach the disk after later operations, and only fsync orders them. Directory operations are not reordered: they stay journaled and in order, as on ext4 and XFS. Which pages survive is drawn from a stream keyed by the crash itself, its time or its `crash_on_io` disk operation, so shrinking away other faults does not change it.
- `Sync` and `Datasync` behave the same: both make every pending write to the file durable, and neither makes the file's directory entry durable, which is what `strict_dirs` models.
- With `strict_dirs`, creating, renaming or unlinking a file survives a crash only after its parent directory is fsynced. This is strict POSIX, the model used by ALICE and CrashMonkey. Without it, directory entries are durable as soon as they are made, which matches ext4 and XFS in practice.
- With `fsync_eio`, a file `Sync` can fail with `EIO`. The unsynced writes stay readable but are dropped from durable storage, and the next `Sync` succeeds. These are Linux's post-2018 "fsyncgate" semantics.

See [faults.md](faults.md) for every fault type.

## Network

Connections are ordered byte streams with seeded per-chunk latency. Partitions hold traffic until they heal, and a crashed node's connections are reset. Every connection is a virtual file registered with Go's own network poller, so blocking reads, deadlines and `Accept` behave exactly like `net`, and nothing polls. See [networking.md](networking.md).

## Crashes and restarts

Every incarnation of a node is a fresh module instance. A crash kills the whole process at a host call, including in the middle of a disk operation with `crash_on_io`, then applies the disk durability model. A restart instantiates the module again over the surviving disk. With `supervise`, a node that exits with an error is restarted the way systemd or Kubernetes would restart it.

## Checking

A run fails when:
- any node calls `sim.Fail`, fails `sim.Assert` or `sim.Always`, or reaches `sim.Unreachable`;
- the workload exits non-zero;
- a node exits or panics, unless `supervise` restarts it (Go panics always fail);
- every node is blocked with no timer or pending message (deadlock);
- a node runs longer than `watchdog` of real time without yielding (livelock or busy loop);
- the recorded `sim.Put` / `sim.Get` history is not linearizable.

A sweep also fails when a cataloged property is never reached, or a `Sometimes` is never true, in any seed.

See [properties.md](properties.md).

## Shrinking

`seedfall shrink` removes faults one at a time for as long as the run still fails the same way (same class, node, property and message, ignoring numbers), so it never trades the bug for a different one. It then writes the minimal schedule to `.seedfall/seed-N.faults.json`. Faults that happen at run time, such as a crash during the N-th disk operation or an fsync EIO, are recorded with their operation index so the schedule replays exactly.
