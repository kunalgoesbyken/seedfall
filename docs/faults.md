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
| `faults_after_setup` | `false` | Suppress all faults until a node calls `sim.SetupComplete()` |
| `supervise` | `false` | Restart a node that exits with an error, after `restart_after`. Go panics (exit code 2) still fail the run |
| `net_overlay` | `false` | Route plain `net.Listen` / `net.Dial` through the simulated network. See [networking.md](networking.md) |
| `watchdog` | `30s` | Real-time limit for one node to run without yielding. Catches busy loops and livelocks |
| `preempt.interleave` | `false` | Preempt the running goroutine after a seeded random number of function calls, so goroutines inside a node interleave at seeded points. See [how-it-works.md](how-it-works.md#preemption) |

Every node sees the environment variables `SEEDFALL=1` and `SEEDFALL_NODE=<name>`, so `sim.Enabled()` and `sim.Node()` work.

## Faults

All faults come from the seed. Faults that depend on what the program does, such as which disk operation a crash lands on, are recorded when they happen so that shrinking and replay are exact.

| Field | Default | Meaning |
|---|---|---|
| `crash_per_sec` | `0` | Rate of crashes per node, at random points in time |
| `restart_after` | `["10ms", "1s"]` | Downtime after a crash, and the delay before a supervised restart |
| `partition_per_sec` | `0` | Rate of network partitions. A partition splits all nodes, workload included, into two random sides |
| `partition_for` | `["50ms", "2s"]` | Partition duration. Traffic across the cut is held and delivered after the heal |
| `torn_writes` | `false` | A crash keeps a random in-order prefix of unsynced writes, possibly ending in a partial write |
| `crash_on_io` | `0` | Probability that a node crashes *during* each disk operation. This is how you hit the window between a write and its fsync |
| `fsync_eio` | `0` | Probability that a file fsync fails with `EIO`. The unsynced writes stay readable but are dropped from durable storage, and the next fsync succeeds (Linux "fsyncgate" semantics) |
| `strict_dirs` | `false` | Directory entries survive a crash only after the parent directory is fsynced (strict POSIX) |
| `clock_skew` | `0` | Each node's wall clock is offset by a seeded amount in `[-skew, +skew]`. The monotonic clock is untouched, so timers and `time.Since` are unaffected |
| `pause_per_sec` | `0` | Rate of process pauses per node. A paused node stops running, like a long GC pause or a stalled VM, then resumes without crashing. Its connections stay open and messages queue up |
| `pause_for` | `["100ms", "3s"]` | Pause duration |
| `clock_jump_per_sec` | `0` | Rate of wall-clock jumps per node. Each jump moves the node's wall clock forward or back by up to `clock_jump_max`; the monotonic clock is untouched |
| `clock_jump_max` | `10s` | Largest clock jump in either direction |
| `swarm` | `false` | Swarm testing. Each seed turns every fault type on or off and rescales its rate between 0 and 2× the configured value, so faults that hide each other get explored separately |

## Choosing rates

Start with crashes and torn writes, then add `crash_on_io` around `0.005`, so a few hundred disk operations per run produce one or two mid-operation crashes. Add `fsync_eio` and `strict_dirs` once the basic durability path passes. Turn on `swarm` for long CI sweeps. Use property coverage ([properties.md](properties.md)) to confirm the faults are actually reaching the code you care about.

## Durations

Every duration is a Go duration string, such as `"250ms"` or `"2s"`. Ranges are two-element arrays.
