# Configuration and faults

This is the reference for `seedfall.json`: every field, and every fault seedfall can inject, such as crashes, partly written disk writes, lost network messages and clock jumps. Use it to look up what a setting does or to design the failures you want to test. If you are new, start with [getting-started.md](getting-started.md).

A world is described in a JSON file, `seedfall.json` by default. Paths inside it are relative to the file. One file can extend another, hold named profiles selected with `-profile`, take `-set path=value` overrides and start from a fault preset such as `"faults": "resilient"`; see [config.md](config.md).

```json
{
  "nodes": [
    { "name": "n1", "pkg": "./node", "args": ["-id", "n1"] },
    { "name": "n2", "pkg": "./node", "args": ["-id", "n2"] }
  ],
  "workload": { "name": "client", "pkg": "./client" },
  "duration": "60s",
  "latency": ["1ms", "5ms"],
  "supervise": true,
  "faults": {
    "crash_per_sec": 0.5,
    "restart_after": ["10ms", "500ms"],
    "partition_per_sec": 0.2,
    "partition_for": ["50ms", "1s"],
    "torn_writes": true,
    "crash_on_io": 0.005,
    "fsync_eio": 0.01,
    "strict_dirs": true,
    "clock_skew": "200ms",
    "pause_per_sec": 0.2,
    "clock_jump_per_sec": 0.05,
    "buggify": true,
    "swarm": true
  }
}
```

## World

| Field | Default | Meaning |
|---|---|---|
| `nodes[]` | | Crashable nodes. Each has a `name`, then either `pkg` (a Go package, built for wasip1 automatically) or `wasm` (a prebuilt module), plus optional `args`, `env`, `faults`, `disk`, `files` and `upgrade_to`. `disk` and `files` copy a host directory and host files onto the node's disk before it first starts, durable and synced, and `upgrade_to` names a package the node runs from its first restart on ([seeded disks](config.md#seeded-disks-and-files), [upgrades](config.md#upgrades)). `"faults": false` keeps every fault away from the node: it is never crashed, paused, slowed or skewed, its disk never fails, and partitions and clogs never cut its links, like Antithesis's fault exclusions. `sim.Buggify` sites in its own code still fire |
| `workload` | | The checker. Same fields as a node. It takes none of the per-node faults (crashes, pauses, clock skew and jumps, slowdowns, resets, disk faults) unless `faults.include_workload` or its own `"faults": true` says so; partitions and clogs reach it like any node. The run passes when it exits 0 |
| `workloads[]` | | Several workloads instead of, or next to, `workload`. Each is a client like `workload`, and the run passes when every one of them has exited 0. See [Faults on the workload](#faults-on-the-workload) |
| `finally` | | A check that starts only after every workload has exited 0, with all faults stopped and the network healed. Same fields as a node; it never takes faults. The run passes when it exits 0, and its properties count like any other. See [properties.md](properties.md#liveness-recovery-and-finally-checks) |
| `liveness.within` | `0s` (off) | Liveness mode. At `duration` the faults stop, partitions, clogs, slowdowns and pauses end, and crashed nodes restart at once; then the workload (and the `finally` check, if any) must finish within `within`, or the run fails with class `liveness` |
| `duration` | `30s` | Virtual-time horizon, and with `liveness` the end of the fault period. A run that reaches it without failing counts as passed and is reported as "hit the time horizon"; with `liveness`, reaching it starts the recovery phase instead |
| `latency` | `["1ms", "10ms"]` | Per-chunk network delay range |
| `links[]` | | Per-link latency overrides, each `{"from": "n1", "to": "n2", "latency": ["20ms", "80ms"]}`. Links are one-way; leave out `from` or `to` to match any node, so `{"to": "n3", ...}` slows everything sent to `n3`. The first matching entry wins. See [Network faults](#network-faults) |
| `faults_after_setup` | `false` | Suppress all faults until a node calls `sim.SetupComplete()` |
| `supervise` | `false` | Restart a node that exits with an error, after `restart_after`. Go panics (exit code 2) still fail the run, and so does a workload that exits with an error, even under `include_workload` |
| `net_overlay` | `false` | Route plain `net.Listen` / `net.Dial` through the simulated network. See [networking.md](networking.md) |
| `compat_overlay` | `false` | Build dependencies that need `mmap` or `flock` with `wasip1` shims: a shim for bbolt, pebble or badger, or the generic shim for any dependency that calls `Mmap` or `Flock`. See [compat.md](compat.md) |
| `watchdog` | `30s` | Real-time limit for one node to run without yielding. Catches busy loops and livelocks. With `preempt.loops`, a busy loop parks like any other node and a run that never finishes fails on `liveness` instead, which does not depend on the host |
| `preempt.strategy` | `none` | When the running goroutine is preempted inside a node: `none` only at the end of a 4,194,304-call time slice, `random` after a seeded 1 to 4,096 counted calls, `pct` at `depth` - 1 seeded change points per node, each recorded as a `preempt` fault, `calls` every `preempt.calls` counted calls. See [how-it-works.md](how-it-works.md#strategies) |
| `preempt.interleave` | `false` | Shorthand for `"strategy": "random"`. While it is `true`, `strategy` can only be `random` or `none`, and `none` still means `random`, so a profile that switches strategy sets `interleave` to `false` |
| `preempt.depth` | `3` | `pct`: the bug depth d to look for, giving d - 1 change points per node |
| `preempt.steps` | `1048576` | `pct`: the change points are drawn over each node's first `steps` counted calls. The trace's last `pct:` line shows how many each node ran |
| `preempt.calls` | `4096` | `calls`: counted calls between preemptions, at most 4,194,304 |
| `preempt.loops` | `false` | Count loop iterations as well as function calls, so a loop without calls is preempted, uses virtual time and lets other nodes run. Costs guest CPU. See [how-it-works.md](how-it-works.md#loops) |

Every node sees the environment variables `SEEDFALL=1` and `SEEDFALL_NODE=<name>`, so `sim.Enabled()` and `sim.Node()` work.

## Faults

All faults come from the seed. Faults that depend on what the program does, such as which disk operation a crash lands on, are recorded when they happen so that shrinking and replay are exact.

| Field | Default | Meaning |
|---|---|---|
| `preset` | | Start from a built-in fault level, `none`, `durability`, `resilient`, `high_availability` or `everything`, each adding to the one before; fields set next to it override it, and `"faults": "resilient"` is shorthand for `{"preset": "resilient"}`. The values each level sets are in [config.md](config.md#fault-presets) |
| `crash_per_sec` | `0` | Rate of crashes per node, at random points in time |
| `restart_after` | `["10ms", "1s"]` | Downtime after a crash, and the delay before a supervised restart |
| `partition_per_sec` | `0` | Rate of network partitions. A partition splits all nodes, workload included, into groups that cannot reach each other. A new partition replaces the previous one |
| `partition_for` | `["50ms", "2s"]` | Partition duration. TCP traffic across the cut is held and delivered after the heal; UDP datagrams are dropped |
| `partition_topologies` | `["half"]` | Shapes a partition can take; each partition picks one at random. `half` (two random sides), `one` (isolate one node), `majority` (a random majority against the rest), `majorities_ring` (every node sees a majority, and no two nodes see the same one) and `random_groups` (two or more random groups). See [Network faults](#network-faults) |
| `clog_per_sec` | `0` | Rate of clogs. A clog hits one direction of one link, or everything a node sends, receives, or both, and lasts `clog_for` |
| `clog_for` | `["100ms", "2s"]` | Clog duration, also the length of a swizzle |
| `clog_modes` | `["stop", "slow", "jam"]` | Modes a clog can take; each clog picks one at random. `stop` refuses new connections and holds traffic, `jam` holds connections and traffic without any error, `slow` adds `clog_delay` to every message. `stop` and `jam` drop UDP datagrams instead of holding them |
| `clog_delay` | `["10ms", "200ms"]` | Extra latency per message of a `slow` clog, drawn once per clog |
| `swizzle_per_sec` | `0` | Rate of swizzles (FoundationDB's swizzle-clogging): a random subset of nodes is clogged one node at a time, then unclogged in a different random order |
| `reset_per_sec` | `0` | Rate of connection resets per node. Every open connection of the node is reset at once, as if a middlebox dropped them; the process keeps running and both ends see `ECONNRESET` |
| `udp_loss` | `0` | Probability that a UDP datagram is dropped. Each loss is recorded as a `udp_drop` fault with the sender and its datagram number. See [UDP datagrams](#udp-datagrams) |
| `udp_dup` | `0` | Probability that a UDP datagram is delivered twice, the copies with independent latencies. Each one is recorded as a `udp_dup` fault |
| `slow_per_sec` | `0` | Rate of slowdowns per node: a gray failure where the node stays up but everything it does takes longer |
| `slow_for` | `["500ms", "5s"]` | Slowdown duration |
| `slow_factor` | `10` | Largest slowdown. Each slowdown picks a factor between 1 and this value |
| `torn_writes` | `false` | A crash keeps a random in-order prefix of unsynced writes, possibly ending in a partial write |
| `reorder_writes` | `false` | A crash writes dirty pages back out of order, as the kernel does between fsyncs, within the limits of `fs_model`. This finds a commit record or header that reaches the disk before the data it points to. Combine with `torn_writes` to also tear the last write kept in each page. See [File system models](#file-system-models) |
| `fs_model` | `ext4_ordered` | Which crash states `reorder_writes` may produce: `ext4_ordered`, `page_independent` or `dir_reorder`, which needs `strict_dirs`. Setting it without `reorder_writes` is an error. See [File system models](#file-system-models) |
| `crash_on_io` | `0` | Probability that a node crashes *during* each disk operation. This is how you hit the window between a write and its fsync |
| `fsync_eio` | `0` | Probability that a file fsync fails with `EIO`. The unsynced writes stay readable but are dropped from durable storage, and the next fsync succeeds (Linux "fsyncgate" semantics) |
| `disk_bytes` | `0` | Size of each node's disk in bytes; `0` means unlimited. A write that does not fit writes what fits and returns the short count, and the next write fails with `ENOSPC`. See [Disk faults](#disk-faults) |
| `write_eio` | `0` | Probability that a file write fails with `EIO` and writes nothing |
| `write_enospc` | `0` | Probability that a file write fails with `ENOSPC` and writes nothing, however much space is free, as with a quota or a thin-provisioned volume |
| `read_eio` | `0` | Probability that a file read fails with `EIO` |
| `read_corrupt` | `0` | Probability that a file read returns one flipped bit. The disk holds the right bytes, so reading again returns them |
| `lost_write` | `0` | Probability that a file fsync succeeds but one 4 KiB page it should have written keeps its old content on disk. The program keeps reading the new content until a crash |
| `misdirected_write` | `0` | Probability that a file fsync succeeds but one page lands at another page offset of the same file, overwriting what was there, while the intended page keeps its old content |
| `corrupt_on_crash` | `false` | Each crash flips one bit in every file whose pending writes cover bytes that are on disk after the crash, somewhere in those bytes, as a sector half-written when the power failed |
| `bitrot_per_sec` | `0` | Rate of bit rot per node: one bit of durable data, in a random file at a random offset, flips |
| `disk_latency` | `["0s", "0s"]` | Extra time each fsync takes, drawn per fsync. Slow nodes stretch it |
| `mmap_early_writeback` | `0` | Probability that a dirty page of a writable shared mapping is already written back when a node crashes. Needs a shared mapping through `compat_overlay`. Recorded as a `mmap_early_writeback` fault. See [compat.md](compat.md#early-writeback-of-mapped-pages) |
| `strict_dirs` | `false` | Directory entries survive a crash only after the parent directory is fsynced (strict POSIX) |
| `clock_skew` | `0` | Each node's wall clock is offset by a seeded amount in `[-skew, +skew]`. The monotonic clock is untouched, so timers and `time.Since` are unaffected |
| `pause_per_sec` | `0` | Rate of process pauses per node. A paused node stops running, like a long GC pause or a stalled VM, then resumes without crashing. Its connections stay open and messages queue up |
| `pause_for` | `["100ms", "3s"]` | Pause duration |
| `clock_jump_per_sec` | `0` | Rate of wall-clock jumps per node. Each jump moves the node's wall clock forward or back by up to `clock_jump_max`; the monotonic clock is untouched |
| `clock_jump_max` | `10s` | Largest clock jump in either direction |
| `buggify` | `false` | Let `sim.Buggify` and `sim.BuggifyWithProb` sites in your code fire. Each site is enabled in a quarter of runs and then fires at its own rate; every firing is recorded as a `buggify` fault with its node, site and hit number. See [buggify.md](buggify.md) |
| `include_workload` | `false` | Give the workloads the per-node faults too: crashes, pauses, clock skew and jumps, slowdowns, connection resets and disk faults. A crashed workload restarts as a fresh process after `restart_after`, with the same name and its disk as the crash left it. See [Faults on the workload](#faults-on-the-workload) |
| `swarm` | `false` | Swarm testing. Each seed turns every fault type on or off and rescales its rate between 0 and 2× the configured value, so faults that hide each other get explored separately |

## Faults on the workload

By default the workload is the one process that never crashes, so a client's own retry, idempotency and session logic is never tested: what happens when a client restarts halfway through a write and sends it again? Set `"include_workload": true` to change that:

```json
"workload": { "name": "client", "pkg": "./client" },
"faults": { "include_workload": true, "crash_per_sec": 0.3, "pause_per_sec": 0.3 }
```

- A crashed workload restarts as a fresh process after `restart_after`, like a node. It keeps its name, so `sim.Node()` and any client id derived from it stay the same, and it finds its disk as the crash left it. A client that saves its progress there can resume; one that keeps it in memory starts over.
- A workload crash does not end the run. The run passes only when the last incarnation of every workload exits 0.
- Operations the crashed workload had recorded with `sim.Put`, `sim.Get`, `sim.Invoke` or `sim.Txn` and not yet completed stay open, and the trace says how many (`client#1 died with 2 operations in flight, their outcome is unknown`). The history check treats an open write as one that may take effect at any time after it started, and drops open reads, so the check stays sound under every [model](properties.md#other-models).
- Per node, `"faults": true` or `false` overrides the default: `true` on one of several workloads faults only that one, and `false` on a node keeps it out of every fault, network faults included.

`workloads[]` runs several clients side by side, each its own process, so one can crash while the others keep going:

```json
"workloads": [
  { "name": "writer", "pkg": "./client", "args": ["-mode", "write"], "faults": true },
  { "name": "reader", "pkg": "./client", "args": ["-mode", "read"] }
]
```

[`examples/liveness`](../examples/liveness) has a client that saves its progress and resends the add in flight after a crash. Without request deduplication on the server, one crash during an add applies it twice.

## Network faults

Every network fault is directional underneath: it decides what happens to traffic from one node to another. A connection needs both directions to open, so a stopped direction either way refuses a dial, and a jammed one holds it.

| Mode | New connections | Data in flight | Errors the program sees |
|---|---|---|---|
| partition, or clog `stop` | refused | held, delivered after the heal | `ECONNREFUSED` on dial |
| clog `jam` | accepted only after the clog lifts | held, delivered after the clog lifts | none, only timeouts |
| clog `slow` | accepted `clog_delay` later when the slow direction points at the listener | arrives `clog_delay` later, in order | none |

A clog targets one of four shapes, chosen at random: one link in one direction (`n1 -> n2`), or a node's inbound, outbound, or inbound and outbound traffic. A one-way partition, where `n1` can hear `n2` but `n2` cannot hear `n1`, is a `stop` clog on one link or an outbound clog on one node. Faults stack: a slow clog over a partition adds its delay to the held traffic.

Partition topologies follow Jepsen's nemeses. `half` is the original random split and stays the default, so existing seeds keep their schedules. `majority` needs at least 3 nodes and `majorities_ring` at least 4, workload included; the config is rejected otherwise. A ring partition is written as overlapping groups, and two nodes can talk only if some group holds both.

A slowdown by factor F stretches everything the node waits for: its timers and sleeps take F times as long, every message it sends or receives takes F times its latency, and a goroutine that spins is parked F times as long. The node still reads the true time, so it sees its own timeouts fire late, the way an overloaded machine does. CPU work between two waits stays free, as it is for every node in seedfall.

`sim.QuietFor` and the recovery phase of `liveness` heal every partition, clog and slowdown at once.

In a fault schedule, the new kinds look like this:

```json
{ "at": "1.2s", "kind": "partition", "groups": [["n1", "n2"], ["n2", "n3"], ["n3", "client"], ["client", "n1"]], "until": "2s" }
{ "at": "1.5s", "kind": "clog", "from": "n1", "to": "n2", "mode": "stop", "until": "3s" }
{ "at": "1.6s", "kind": "clog", "node": "n3", "dir": "out", "mode": "slow", "delay": "80ms", "until": "2.4s" }
{ "at": "2.1s", "kind": "net_reset", "node": "n2", "until": "2.1s" }
{ "at": "2.5s", "kind": "slow", "node": "n1", "factor": 4.5, "until": "6s" }
```

`dir` is `in`, `out` or `both`. A swizzle shows up as a run of `both` clogs in `stop` mode. Like every other planned fault, each one replays and shrinks on its own, appears in the trace, `seedfall cause` and the report, and joins swarm testing.

### UDP datagrams

TCP traffic is a byte stream, where the kernel retransmits and deduplicates, so a program never sees a packet dropped or repeated on a connection. A UDP program does. `udp_loss` and `udp_dup` are probabilities per datagram, drawn from their own stream of the seed, so they take no draws from any other fault:

```json
{ "faults": { "udp_loss": 0.05, "udp_dup": 0.02 } }
```

A partition or a clog that stops or jams a link also drops a datagram sent across it, rather than holding it for the heal, and a `slow` clog delays it. Datagrams that a link or a missing socket drops are consequences of the rest of the schedule, so they are not faults of their own: only `udp_loss` and `udp_dup` choices are recorded. They count per sender node, from 1, across restarts:

```json
{ "at": "2.1s", "kind": "udp_drop", "node": "n1", "to": "n2", "op": 17, "until": "0s" }
{ "at": "2.2s", "kind": "udp_dup", "node": "n2", "to": "n3", "op": 9, "until": "0s" }
```

`op` is the number of the datagram the sender sent, and the fault fires on that datagram whatever its destination; `to` is a record of where it went. Both replay exactly, shrink like any fault recorded at runtime, can be edited with `seedfall faults`, and join swarm testing. They do not apply when `faults` is off for either end of the datagram, or during a quiet period or the recovery phase. Semantics of sockets and addresses are in [networking.md](networking.md#udp).

## Disk faults

## File system models

`reorder_writes` turns on out-of-order writeback at a crash. `fs_model` says which states a crash may then leave behind. A model that is too weak reports bugs your file system cannot produce; one that is too strong misses bugs it can. The models follow Table 1 of ALICE ([All File Systems Are Not Created Equal](https://www.usenix.org/system/files/conference/osdi14/osdi14-paper-pillai.pdf), OSDI '14), which measured these properties on real file systems.

| `fs_model` | File contents | Directory operations (`strict_dirs`) | Matches |
|---|---|---|---|
| `ext4_ordered` (default) | Appends and truncates of one file persist as an in-order prefix, split at 4 KiB page boundaries. Overwrites of existing bytes persist per page in any order, independently of the appends. Bytes past the surviving file size are not kept | An in-order prefix of the pending operations, under `torn_writes` | ext4 `data=ordered` |
| `page_independent` | Any subset of the dirty pages of a file, in order within each page. A later appended page can survive without an earlier one, leaving zeros in the gap | As `ext4_ordered` | Aggressive: useful to find code that depends on append order, but it reaches states ext4 `data=ordered` does not. ALICE records this for ext2, ext3 and ext4 in `writeback` mode, not for `ordered` |
| `dir_reorder` | As `page_independent` | Each pending operation survives independently, so a rename can persist without the create before it | ext2 and btrfs, where ALICE found directory operations persist out of order |

Details that follow from these rules:

- Models apply per file. Different files are independent, so an append to one file can survive while an earlier append to another is lost. ALICE records `Append -> Any op` failing on ext4 `ordered` for this reason (delayed allocation).
- Under `ext4_ordered` a crash that keeps an overwrite past the surviving file size keeps nothing of it, because that page was never part of the file on disk.
- A single directory operation is atomic in every model, as on btrfs. ext2 can also tear one rename so that both names or neither survive; that is not modelled.
- Plain `torn_writes` without `reorder_writes` does not use `fs_model`: it keeps an in-order prefix of all pending writes.

[`examples/symswap`](../examples/symswap) shows the difference. It publishes each generation by renaming a symlink over `current`, and fsyncs the new generation's file but not its directory entry first. Under `ext4_ordered` it passes, because a journal keeps directory operations in order. Under `dir_reorder` the rename can survive without the create, and `current` points at nothing.

Crashes, torn writes, `reorder_writes` and `fsync_eio` test what survives a crash. The faults below test what a storage engine does when the disk itself misbehaves: it fills up, it fails an operation, or it hands back or keeps the wrong bytes. These are the paths that WAL recovery and checksum code exist for, and the ones that are hardest to reach with a real disk.

```json
"faults": {
  "crash_per_sec": 0.5,
  "torn_writes": true,
  "disk_bytes": 1048576,
  "write_eio": 0.01,
  "read_eio": 0.01,
  "read_corrupt": 0.05,
  "lost_write": 0.01,
  "misdirected_write": 0.01,
  "corrupt_on_crash": true,
  "bitrot_per_sec": 0.2,
  "disk_latency": ["1ms", "20ms"]
}
```

| Fault | What the program sees | What is on disk afterwards |
|---|---|---|
| `disk_bytes` | A short write, then `ENOSPC` on the next write. Overwrites inside a file always fit, and space comes back when files shrink or are removed | What fitted |
| `write_eio`, `write_enospc` | The write fails | Nothing from that write |
| `read_eio` | The read fails | Unchanged |
| `read_corrupt` | One bit flipped in the bytes returned by one read | Unchanged |
| `lost_write` | fsync succeeds, and reads return the new content | One page has its old content; zeros if the page is new. The next fsync that rewrites the page writes it in full |
| `misdirected_write` | fsync succeeds, and reads return the new content | The intended page has its old content, and another page of the file holds the new one |
| `corrupt_on_crash` | After the restart, one flipped bit per file among the bytes that were being written | Same |
| `bitrot_per_sec` | The flipped bit, from the moment it happens, unless that byte has a pending write | One flipped bit |
| `disk_latency` | fsync takes longer | Unchanged |

How each one is modelled, and where it stops:

- **Disk usage** is the sum of every file's size on the node. Holes left by `Truncate` or by writing past the end count in full, so sparse files use more space than on ext4 or XFS. Directories and metadata use none. A `Truncate` that grows a file is never refused.
- **`ENOSPC` reaches your code as `ENOSPC`.** wazero's WASI layer has no `ENOSPC` and reports `EIO` for it, so seedfall rewrites the result of `fd_write` and `fd_pwrite` when the simulated disk was full, and `errors.Is(err, syscall.ENOSPC)` works in the guest.
- **Read and write errors** apply to file reads and writes only. Directory listings, `stat`, `open` and `Truncate` never fail.
- **Corrupted reads** flip one bit in the buffer of one read, as a bad cable or controller would. A checksum plus a retry handles them; a checksum alone turns them into an error.
- **Lost and misdirected writes** pick one dirty page of the file being fsynced. The page cache keeps the right content, because the drive acknowledged the write, so the program only notices after a crash, or never if the page is rewritten first. A misdirected write stays inside the file; writes landing in another file are not modelled. A file needs at least two pages for a misdirected write.
- **Crash corruption** damages only bytes that were being written when the node crashed. With plain crashes, unsynced writes are dropped, so it matters only where they survive: with `torn_writes`, `reorder_writes` or `crash_on_io`. If the damaged range overlapped older durable data, that data is what flips.
- **Bit rot** picks a byte uniformly over all durable data on the node, so large files are hit more often.
- **Latency** applies to file and directory fsyncs only, not to reads or writes, which go to the page cache. The whole node waits, because a wasip1 guest runs every goroutine on one thread.

Every one of them comes from the seed and replays exactly. The rates are drawn per operation, like `crash_on_io`, and each firing is recorded with its node and operation number: `disk op #N` counts the node's writes, truncates and fsyncs, and `disk read #N` counts its reads. Which page or bit is hit comes from a stream keyed by the seed, the fault kind, the node and that number, so it does not change when shrinking removes other faults. Bit rot is planned in time like a crash. In a fault schedule they look like this:

```json
{ "at": "1.942589003s", "kind": "read_corrupt", "node": "server", "op": 3, "path": "data/kv.log", "offset": 70, "until": "0s" }
{ "at": "912.496ms", "kind": "write_eio", "node": "server", "op": 67, "path": "data/kv.log", "until": "0s" }
{ "at": "2.601158s", "kind": "lost_write", "node": "server", "op": 232, "path": "data/kv.log", "until": "0s" }
{ "at": "5.259168s", "kind": "misdirected_write", "node": "server", "op": 558, "path": "data/kv.log", "offset": 4096, "until": "0s" }
{ "at": "657.015787ms", "kind": "bitrot", "node": "server", "path": "data/kv.log", "offset": 22, "until": "657.015787ms" }
{ "at": "36ms", "kind": "mmap_early_writeback", "node": "server", "op": 3, "path": "data/pages", "offset": 65536, "until": "0s" }
```

The other kinds are `write_enospc` and `read_eio`. A recorded fault fires at the operation its `kind`, `node` and `op` name, and bit rot fires at `at`. `path`, `offset` and `target` are filled in by the run so that you can read the schedule, and are left out when they are 0. `seedfall inspect` lists every damaged range of a file under `corrupted ranges`, and with `disk_bytes` shows how full each disk is. Disk usage and latency are settings, not faults, so swarm testing leaves them alone and toggles the rest.

[`examples/kvlog`](../examples/kvlog) shows each one: a corrupted read changes an acknowledged value, a checksum with a re-read fixes it, and a lost write, a misdirected write or bit rot breaks a store that keeps one copy, with or without checksums.

## Editing a schedule

A fault schedule (`.seedfall/seed-N.faults.json` from `shrink`, an artifact's `faults.json`, or a pinned regression) is a list of faults that a replay applies instead of drawing new ones. Changing one fault and replaying is the quickest way to test an explanation of a failure. `seedfall faults` makes the change and checks it:

```sh
seedfall faults -c seedfall.always.json -seed 1 -faults torntail.faults.json -remove 1 -out hypothesis.json
seedfall replay -c seedfall.always.json -seed 1 -faults hypothesis.json
```

```
seed 1, 1 faults from torntail.faults.json, wrote hypothesis.json
  1    802ms  crash server, restart at 802ms
duration 991ms
replay: seedfall replay -c seedfall.always.json -seed 1 -faults hypothesis.json
```

On kvlog's `seedfall.always.json`, seed 1 fails with `torntail.faults.json`, still fails with the crash during disk op #56 moved one operation earlier (`-shift 1=-1`), and passes once that crash is removed. That pass is within the schedule's shrunk 991ms, and the replay prints a note saying so: it shows the crash is necessary for this failure, not that the store is fixed.

| Flag | Meaning |
|---|---|
| `-faults` | The schedule to start from. Without it, `.seedfall/seed-N.faults.json` next to the config, or, when that file does not exist, the faults seed N draws on its own, which takes one run |
| `-remove N` | Remove the fault at position N, counting from 1 as listed. Repeatable |
| `-shift N=AMOUNT` | Move fault N. A timed fault moves by a signed duration (`2=-50ms`), together with its `until`. A fault that fires at a disk operation, read, buggify hit, counted call or udp datagram (`crash_io`, `fsync_eio`, `write_eio`, `write_enospc`, `read_eio`, `read_corrupt`, `lost_write`, `misdirected_write`, `buggify`, `preempt`, `udp_drop`, `udp_dup`, `mmap_early_writeback`) moves by a signed count of those (`1=+2`), because its `at` only records when it happened. Repeatable |
| `-add JSON` | Add a fault in the format shown in this page, for example `{"at":"1s","kind":"pause","node":"server","until":"1.5s"}`. Repeatable |
| `-out` | Write the result here. Without it, the schedule is written back to the file it came from |

Positions in `-remove` and `-shift` always refer to the schedule as it was before the edit. An edited schedule drops the `shrunk_from` and `shrink_runs` that `shrink` recorded, since it is no longer what shrink found. The result is sorted by `at` and checked against the config before anything is written:

| Check | Applies to |
|---|---|
| `kind` is a fault kind seedfall knows | every fault |
| `node`, `from`, `to`, `side` and `groups` name nodes of the config | every fault |
| `at` is not negative | every fault |
| `at` is not after the end of the run: the schedule's `duration`, else the config's | every fault except those that fire at a disk operation, read, buggify hit, counted call or udp datagram, whose `at` only records when it happened |
| `until` is not before `at` | `crash`, `crash_io`, `partition`, `pause`, `clog`, `slow` |
| `side` or `groups`, not both | `partition` |
| `mode` is `stop`, `slow` or `jam`; `dir` is `in`, `out` or `both` with a `node`, or empty with two different nodes in `from` and `to` | `clog` |
| `node` is set | every kind except `partition` and `clog` |
| `factor` is at least 1 | `slow` |
| `op` is at least 1, a `buggify` fault names its `site`, and a `preempt` fault needs `preempt.strategy` `pct` in the config | faults that fire at a disk operation, read, buggify hit, counted call or udp datagram |

Without any edit, `seedfall faults` only lists and checks the schedule, and writes it only when it came from the seed's own faults, so that there is a file to edit. Over MCP, `seedfall_faults_edit` takes the same edits as JSON ([agents.md](agents.md#tools)).

A schedule file can also hold `branches`, which [`seedfall explore`](explore.md) writes for a failure it found by branching:

```json
"branches": [{ "at": "921.003003ms", "key": 257366709774643251 }, { "at": "1.2s", "key": 9, "stream": "sched" }]
```

Each branch re-keys the seed's random streams at the first scheduler step at or after `at`, in order. Without `stream` it re-keys every stream; with one of `sched`, `net`, `disk`, `io`, `udp`, `disklatency`, `mutant` or `random` it re-keys only that one. Re-keying `io` or `udp` changes nothing in a schedule, because those streams only decide faults and a schedule already fixes them; `mutant` matters only under `seedfall mutate`. `replay`, `shrink`, `inspect`, `debug`, `logs` and `pin` all honor them, and `at` values must not decrease.

## Choosing rates

Start with crashes and torn writes, then add `crash_on_io` around `0.005`, so a few hundred disk operations per run produce one or two mid-operation crashes. Add `fsync_eio`, `strict_dirs` and `reorder_writes` once the basic durability path passes. For consensus and leader-election code, add `partition_topologies` beyond `half`, then `clog_per_sec` and `slow_per_sec`: one-way links and slow leaders are the gray failures that break leases and stale-read checks. For a storage engine, add `disk_bytes` and `write_eio` to test the error paths of every write, then `read_corrupt` and `corrupt_on_crash` to test checksums. `lost_write`, `misdirected_write` and `bitrot_per_sec` lose data that a single copy cannot get back, so use them on replicated systems, or to check that a single node notices and stops instead of serving wrong data. Turn on `buggify` once you have marked sites in your code that should sometimes take the rare path. Turn on `swarm` for long CI sweeps. Use property coverage ([properties.md](properties.md)) to confirm the faults are actually reaching the code you care about.

## Durations

Every duration is a Go duration string, such as `"250ms"` or `"2s"`. Ranges are two-element arrays.
