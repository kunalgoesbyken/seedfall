# How seedfall works

## One process, one clock, many nodes

seedfall compiles every node with the stock Go toolchain for `GOOS=wasip1` and runs each one as its own [wazero](https://wazero.io) module instance. On `wasip1` the Go runtime is single-threaded and has no async preemption, and every source of nondeterminism reaches the outside world through three WASI calls. seedfall implements all three in the host, and adds a deterministic preemption point of its own (below):

| WASI call | seedfall behavior |
|---|---|
| `random_get` | A ChaCha8 stream derived from the seed and the node's incarnation. This fixes map iteration order, `select` choice, `math/rand` and crypto randomness. |
| `clock_time_get` | A virtual clock shared by every node. Every read advances it by one nanosecond, so two `time.Now()` calls in a row never return the same value, as on real hardware; wall and monotonic clocks advance together. With `clock_skew` and `clock_jump`, each node's wall clock is offset by a seeded amount; the monotonic clock is never skewed. |
| `poll_oneoff` | Parks the node until its earliest timer or its next network arrival. |

A node gives up control when all of its goroutines are blocked, or when it is preempted after a long run without blocking (see [Preemption](#preemption)). The scheduler runs exactly one node at a time, picks the next one by earliest wake-up time, breaks ties with the seed, and jumps virtual time forward. Ten seconds of `time.Sleep` cost no real time.

There is no `linkname`, no source rewriting and no forked runtime, so new Go releases keep working.

## Preemption

Real Go preempts a goroutine that has run for 10ms. On `wasip1` nothing does, so a goroutine that spins without a syscall would starve every other goroutine in its node, and its node would never yield. seedfall loads each Go module with a call counter added to the start of every function. After 4,194,304 function calls without the node blocking, the counter calls back into the host, which triggers the Go runtime's own cooperative preemption path for the running goroutine (the same `stackguard0` request the runtime's `sysmon` makes on other platforms) and parks the node for 10ms of virtual time, so other nodes run too. The count is part of the execution, so preemption points are as deterministic as everything else. A loop that makes no function call at all is still not preempted.

With `"preempt": {"interleave": true}`, the counter is also re-armed after a seeded random number of calls, up to 4,096, and every expiry preempts the running goroutine. Goroutines inside a node then interleave at seeded points, so a check-then-act race between goroutines shows up as an ordinary failing seed that replays exactly. See [`examples/interleave`](../examples/interleave).

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
- With `disk_bytes`, each node's disk has a fixed size. A write that does not fit is cut short and the next one fails with `ENOSPC`.
- `write_eio`, `write_enospc`, `read_eio` and `read_corrupt` fail or corrupt single reads and writes. `lost_write` and `misdirected_write` make a successful fsync leave one page with its old content, or put it at the wrong offset, while the page cache keeps the right bytes. `corrupt_on_crash` flips a bit in the bytes being written at a crash, `bitrot_per_sec` flips durable bits over time, and `disk_latency` makes every fsync wait. Each per-operation fault is recorded with the node's disk operation or disk read number, so it replays and shrinks like `crash_on_io`. See [Disk faults](faults.md#disk-faults).

See [faults.md](faults.md) for every fault type.

## Network

Connections are ordered byte streams with seeded per-chunk latency. Partitions and `stop` or `jam` clogs hold traffic until they heal, `slow` clogs and slow nodes add latency, and a crashed node's connections are reset, as are a live node's when a `net_reset` fault fires. Every fault is a rule on one direction of a link, so one-way partitions fall out of the same code. Every connection is a virtual file registered with Go's own network poller, so blocking reads, deadlines and `Accept` behave exactly like `net`, and nothing polls. See [networking.md](networking.md).

## Crashes and restarts

Every incarnation of a node is a fresh module instance. A crash kills the whole process at a host call, including in the middle of a disk operation with `crash_on_io`, then applies the disk durability model. A restart instantiates the module again over the surviving disk. With `supervise`, a node that exits with an error is restarted the way systemd or Kubernetes would restart it.

## Checking

A run fails when:
- any node calls `sim.Fail`, fails `sim.Assert` or `sim.Always`, or reaches `sim.Unreachable`;
- a `sim.Eventually` condition is still false at its deadline, or, with `liveness`, the workload or the `finally` check has not finished within `liveness.within` of the recovery phase (liveness);
- the workload or the `finally` check exits non-zero;
- a node exits or panics, unless `supervise` restarts it (Go panics always fail);
- every node is blocked with no timer or pending message (deadlock);
- a node runs longer than `watchdog` of real time without yielding (livelock or busy loop);
- the recorded `sim.Put` / `sim.Get` / `sim.Invoke` history is not linearizable under the configured model, or the `sim.Txn` history has an isolation anomaly.

A sweep also fails when a cataloged property is never reached, or a `Sometimes` is never true, in any seed.

See [properties.md](properties.md).

## Shrinking

`seedfall shrink` removes faults one at a time for as long as the run still fails the same way (same class, node, property and message, ignoring numbers), so it never trades the bug for a different one. It then writes the minimal schedule to `.seedfall/seed-N.faults.json`. Faults that happen at run time, such as a crash during the N-th disk operation or an fsync EIO, are recorded with their operation index so the schedule replays exactly.

## Speed

Virtual time never waits on the wall clock. When every node is blocked, the scheduler jumps straight to the next timer, message or fault; the only real-time limit is the `watchdog`. Real time therefore goes to three things:

- **Guest CPU.** Nodes run as wasm compiled to native code by wazero, with a check on every loop iteration so the watchdog can stop a busy loop. That check costs about a third of guest CPU and is what makes livelocks reportable instead of hanging the run. Host calls (clock, `poll_oneoff`, files, network) are a few percent.
- **Booting nodes.** Every start and restart is a fresh module instance: wazero sets up memory and data segments, then the Go runtime and your `main` run. For nats-server this is 0.1-0.3s per start, so short seeds with many crashes are dominated by boots.
- **Compiling, once.** Each guest is compiled once per process and shared by every seed and every node that runs the same binary. Compiled code is cached in `.seedfall/cache`, so later processes only load it. The cache keeps the 8 most recently used sets of binaries and deletes older ones, so rebuilding a guest many times does not fill the disk.

A busy 3-node NATS JetStream cluster under crashes and partitions simulates about 2.5-3x faster than real time; a JetStream node that mostly waits on its client about 7x. Seeds are independent, so `seedfall run -j N` runs N at once.
