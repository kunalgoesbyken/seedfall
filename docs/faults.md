# Configuration and faults

A world is described in a JSON file, `seedfall.json` by default. Paths inside it are relative to the file.

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
| `nodes[]` | | Crashable nodes. Each has a `name`, then either `pkg` (a Go package, built for wasip1 automatically) or `wasm` (a prebuilt module), plus optional `args` and `env` |
| `workload` | | The checker. Same fields as a node. It is never crashed, and the run passes when it exits 0 |
| `duration` | `30s` | Virtual-time horizon. A run that reaches it without failing counts as passed and is reported as "hit the time horizon" |
| `latency` | `["1ms", "10ms"]` | Per-chunk network delay range |
| `links[]` | | Per-link latency overrides, each `{"from": "n1", "to": "n2", "latency": ["20ms", "80ms"]}`. Links are one-way; leave out `from` or `to` to match any node, so `{"to": "n3", ...}` slows everything sent to `n3`. The first matching entry wins. See [Network faults](#network-faults) |
| `faults_after_setup` | `false` | Suppress all faults until a node calls `sim.SetupComplete()` |
| `supervise` | `false` | Restart a node that exits with an error, after `restart_after`. Go panics (exit code 2) still fail the run |
| `net_overlay` | `false` | Route plain `net.Listen` / `net.Dial` through the simulated network. See [networking.md](networking.md) |
| `compat_overlay` | `false` | Build dependencies that need `mmap` or `flock` (bbolt, pebble, badger) with `wasip1` shims. See [compat.md](compat.md) |
| `watchdog` | `30s` | Real-time limit for one node to run without yielding. Catches busy loops and livelocks |
| `preempt.interleave` | `false` | Preempt the running goroutine after a seeded random number of function calls, so goroutines inside a node interleave at seeded points. See [how-it-works.md](how-it-works.md#preemption) |

Every node sees the environment variables `SEEDFALL=1` and `SEEDFALL_NODE=<name>`, so `sim.Enabled()` and `sim.Node()` work.

## Faults

All faults come from the seed. Faults that depend on what the program does, such as which disk operation a crash lands on, are recorded when they happen so that shrinking and replay are exact.

| Field | Default | Meaning |
|---|---|---|
| `crash_per_sec` | `0` | Rate of crashes per node, at random points in time |
| `restart_after` | `["10ms", "1s"]` | Downtime after a crash, and the delay before a supervised restart |
| `partition_per_sec` | `0` | Rate of network partitions. A partition splits all nodes, workload included, into groups that cannot reach each other. A new partition replaces the previous one |
| `partition_for` | `["50ms", "2s"]` | Partition duration. Traffic across the cut is held and delivered after the heal |
| `partition_topologies` | `["half"]` | Shapes a partition can take; each partition picks one at random. `half` (two random sides), `one` (isolate one node), `majority` (a random majority against the rest), `majorities_ring` (every node sees a majority, and no two nodes see the same one) and `random_groups` (two or more random groups). See [Network faults](#network-faults) |
| `clog_per_sec` | `0` | Rate of clogs. A clog hits one direction of one link, or everything a node sends, receives, or both, and lasts `clog_for` |
| `clog_for` | `["100ms", "2s"]` | Clog duration, also the length of a swizzle |
| `clog_modes` | `["stop", "slow", "jam"]` | Modes a clog can take; each clog picks one at random. `stop` refuses new connections and holds traffic, `jam` holds connections and traffic without any error, `slow` adds `clog_delay` to every message |
| `clog_delay` | `["10ms", "200ms"]` | Extra latency per message of a `slow` clog, drawn once per clog |
| `swizzle_per_sec` | `0` | Rate of swizzles (FoundationDB's swizzle-clogging): a random subset of nodes is clogged one node at a time, then unclogged in a different random order |
| `reset_per_sec` | `0` | Rate of connection resets per node. Every open connection of the node is reset at once, as if a middlebox dropped them; the process keeps running and both ends see `ECONNRESET` |
| `slow_per_sec` | `0` | Rate of slowdowns per node: a gray failure where the node stays up but everything it does takes longer |
| `slow_for` | `["500ms", "5s"]` | Slowdown duration |
| `slow_factor` | `10` | Largest slowdown. Each slowdown picks a factor between 1 and this value |
| `torn_writes` | `false` | A crash keeps a random in-order prefix of unsynced writes, possibly ending in a partial write |
| `reorder_writes` | `false` | A crash keeps any subset of the dirty 4 KiB pages of each file, keeping writes in order within a page. This finds a commit record or header that reaches the disk before the data it points to. Combine with `torn_writes` to also tear the last write kept in each page. See [how-it-works.md](how-it-works.md#disk) |
| `crash_on_io` | `0` | Probability that a node crashes *during* each disk operation. This is how you hit the window between a write and its fsync |
| `fsync_eio` | `0` | Probability that a file fsync fails with `EIO`. The unsynced writes stay readable but are dropped from durable storage, and the next fsync succeeds (Linux "fsyncgate" semantics) |
| `strict_dirs` | `false` | Directory entries survive a crash only after the parent directory is fsynced (strict POSIX) |
| `clock_skew` | `0` | Each node's wall clock is offset by a seeded amount in `[-skew, +skew]`. The monotonic clock is untouched, so timers and `time.Since` are unaffected |
| `pause_per_sec` | `0` | Rate of process pauses per node. A paused node stops running, like a long GC pause or a stalled VM, then resumes without crashing. Its connections stay open and messages queue up |
| `pause_for` | `["100ms", "3s"]` | Pause duration |
| `clock_jump_per_sec` | `0` | Rate of wall-clock jumps per node. Each jump moves the node's wall clock forward or back by up to `clock_jump_max`; the monotonic clock is untouched |
| `clock_jump_max` | `10s` | Largest clock jump in either direction |
| `buggify` | `false` | Let `sim.Buggify` and `sim.BuggifyWithProb` sites in your code fire. Each site is enabled in a quarter of runs and then fires at its own rate; every firing is recorded as a `buggify` fault with its node, site and hit number. See [buggify.md](buggify.md) |
| `swarm` | `false` | Swarm testing. Each seed turns every fault type on or off and rescales its rate between 0 and 2× the configured value, so faults that hide each other get explored separately |

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

`sim.QuietFor` heals every partition, clog and slowdown at once.

In a fault schedule, the new kinds look like this:

```json
{ "at": "1.2s", "kind": "partition", "groups": [["n1", "n2"], ["n2", "n3"], ["n3", "client"], ["client", "n1"]], "until": "2s" }
{ "at": "1.5s", "kind": "clog", "from": "n1", "to": "n2", "mode": "stop", "until": "3s" }
{ "at": "1.6s", "kind": "clog", "node": "n3", "dir": "out", "mode": "slow", "delay": "80ms", "until": "2.4s" }
{ "at": "2.1s", "kind": "net_reset", "node": "n2", "until": "2.1s" }
{ "at": "2.5s", "kind": "slow", "node": "n1", "factor": 4.5, "until": "6s" }
```

`dir` is `in`, `out` or `both`. A swizzle shows up as a run of `both` clogs in `stop` mode. Like every other planned fault, each one replays and shrinks on its own, appears in the trace, `seedfall cause` and the report, and joins swarm testing.

Not modelled: dropped or duplicated packets. The simulated network carries TCP byte streams, where the kernel retransmits and deduplicates, so a program never sees either. UDP is not simulated (see [networking.md](networking.md)).

## Choosing rates

Start with crashes and torn writes, then add `crash_on_io` around `0.005`, so a few hundred disk operations per run produce one or two mid-operation crashes. Add `fsync_eio`, `strict_dirs` and `reorder_writes` once the basic durability path passes. For consensus and leader-election code, add `partition_topologies` beyond `half`, then `clog_per_sec` and `slow_per_sec`: one-way links and slow leaders are the gray failures that break leases and stale-read checks. Turn on `buggify` once you have marked sites in your code that should sometimes take the rare path. Turn on `swarm` for long CI sweeps. Use property coverage ([properties.md](properties.md)) to confirm the faults are actually reaching the code you care about.

## Durations

Every duration is a Go duration string, such as `"250ms"` or `"2s"`. Ranges are two-element arrays.
