# seedfall

**Deterministic simulation testing for Go.** seedfall runs your services in a simulated world, then crashes them mid-write, drops their unsynced data, tears their writes and partitions their network. When an invariant breaks you get the seed, the exact fault schedule and a full trace, and you can shrink it to the few faults that matter.

- **Stock toolchain.** It compiles your code with `GOOS=wasip1` and runs it on [wazero](https://wazero.io). It needs no `linkname`, no source rewriting and no forked runtime.
- **Unmodified file I/O.** Your code uses plain `os.File`, and seedfall supplies the disk with real fsync semantics.
- **One-line network change.** Create listeners and dialers with `sim.Listen` / `sim.Dial`. Outside the simulator they fall back to `net`.
- **Real processes.** Each node runs as its own wasm instance, so a crash kills the whole process and a restart boots fresh over the surviving disk.
- **Exact replays.** The same seed produces the same run byte for byte, across processes and machines.

```
$ seedfall run -c seedfall.json -seeds 1-500
FAIL seed 9 at t=7.267s (4057 scheduler steps, 7.4s real)
  client#1: acknowledged write lost: k4=v4, leader answers "NONE"

$ seedfall shrink -c seedfall.json -seed 9
faults before the failure (3)        # shrunk from 75
  1.983s  crash n1 during disk op #58, restart at 2.681s
  2.123s  crash n2, restart at 2.957s
  2.405s  crash n3, restart at 2.591s
```

## Why

The bugs that lose data come from rare combinations of events: a crash between write and fsync, a restart that misreads a half-written record, a partition during a leader change. Ordinary tests seldom reach them and production can't reproduce them. Some real cases:

- Jepsen found NATS JetStream 2.12.1 losing 679,153 of 1,367,069 acknowledged writes ([report](https://jepsen.io/analyses/nats-2.12.1)).
- etcd 3.5.0–3.5.5 diverged after a crash during defragmentation, and it went unnoticed for five patch releases.
- Go's `testing/synctest` fakes time only. A request for deterministic scheduling ([golang/go#80316](https://github.com/golang/go/issues/80316)) was closed as not planned.
- The other Go DST tools each require you to give something up. Source-rewriting tools break on new Go releases. Injection-based tools make you refactor your code to take their net and fs. Toolchain forks go unmaintained. Antithesis starts at $17,520/yr.

## How it works

On `wasip1` the Go runtime is single-threaded and has no async preemption. All of its nondeterminism goes through three WASI calls, and seedfall implements each one:

| WASI call | seedfall behavior |
|---|---|
| `random_get` | A ChaCha8 stream derived from the seed. This also fixes map iteration order, `select` choice and `math/rand`. |
| `clock_time_get` | A virtual clock shared by every node. |
| `poll_oneoff` | Parks the node until its earliest timer or its next network arrival. The scheduler then picks the next node and moves virtual time forward. |

A node gives up control only when every one of its goroutines is blocked. The scheduler runs one node at a time and breaks ties with the seed. Ten seconds of `time.Sleep` cost no real time.

**Disk.** An in-memory file system with a durability model:
- `Sync` makes data durable.
- A crash keeps only durable data. With `torn_writes`, it also keeps a random prefix of the pending writes, possibly ending in a partial write.
- A file that was never synced disappears.

**Network.** Ordered byte streams with seeded per-chunk latency:
- Partitions hold traffic until they heal.
- A crash resets the dead node's connections.
- Connections are virtual files registered with Go's own network poller, so blocking reads, deadlines and `Accept` behave exactly as they do with `net`, and nothing polls.

**Faults.** These come from the seed and are separate from scheduling, so removing one barely disturbs the rest of the run:
- `crash`: kill a node while it is idle, then restart it later.
- `crash_io`: kill a node *during* its N-th disk operation. This is how you hit the window between write and fsync.
- `partition`: split the cluster into two sides until a heal time.

**Shrinking.** `seedfall shrink` removes faults one at a time for as long as the run still fails. The result is saved as JSON and replays exactly.

**Linearizability.** Workloads can record key-value operations with `sim.Put` / `sim.Get` from any number of concurrent clients. When the workload finishes, seedfall checks the history with [Porcupine](https://github.com/anishathalye/porcupine). On a violation it writes an interactive timeline to `.seedfall/seed-N.history.html`.

```go
op := sim.Put(clientID, "k1", "v7")
reply, err := client.Put("k1", "v7")
switch {
case err != nil:          op.Unknown()
case reply == "redirect": op.Failed()
default:                  op.Ok("")
}
```

`Unknown` means the write might or might not have taken effect. `Failed` means it definitely didn't. Reads with an unknown result are dropped.

**Strict directories.** With `strict_dirs`, creating, renaming or unlinking a file is durable only after its **parent directory** is fsynced. This follows strict POSIX, the model used by ALICE and CrashMonkey. Without it, directory entries are durable as soon as they're made, which matches ext4/XFS behavior in practice.

## Quickstart

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
```

1. Route networking through `sim`. Leave file I/O as it is.

```go
import "github.com/kunalgoesbyken/seedfall/sim"

ln, err := sim.Listen("tcp", ":7000")
conn, err := sim.Dial("tcp", "server:7000")
sim.Assert(got == want, "acknowledged write lost")
```

2. Describe the world in `seedfall.json`:

```json
{
  "nodes": [
    { "name": "server", "pkg": "./server", "args": ["-sync", "always"] }
  ],
  "workload": { "name": "client", "pkg": "./client" },
  "duration": "60s",
  "latency": ["1ms", "5ms"],
  "faults": {
    "crash_per_sec": 0.5,
    "restart_after": ["10ms", "500ms"],
    "partition_per_sec": 0.2,
    "partition_for": ["50ms", "1s"],
    "torn_writes": true,
    "crash_on_io": 0.005
  }
}
```

3. Run it:

```sh
seedfall run    -seeds 1-5000 -j 8     # parallel sweep, stops at the lowest failing seed
seedfall replay -seed 9                # full trace of one seed
seedfall shrink -seed 9                # minimal fault schedule, saved to .seedfall/
seedfall replay -seed 9 -faults .seedfall/seed-9.faults.json
```

Or sweep from `go test` (CI-friendly; a failure prints the seed, the reason, the trace tail and the replay command):

```go
func TestDurabilityUnderCrashes(t *testing.T) {
	seedfalltest.Sweep(t, "seedfall.json", "1-100")
}
```

The workload is any Go program. It exercises the system and checks invariants. A run **fails** if:
- any node calls `sim.Fail`, fails `sim.Assert` or `sim.Always`, or reaches `sim.Unreachable`;
- the workload exits non-zero;
- a server node exits or panics;
- every node blocks forever (deadlock);
- the real-time watchdog fires (livelock).

### Properties and coverage

A green sweep only means something if the scary paths actually ran. Properties are named by a constant message, aggregated across every seed, and printed after a sweep:

```go
sim.Always(len(log) >= committed, "log never shrinks below the commit index", len(log), committed)
sim.Sometimes(tornTail, "recovery found a torn tail")
sim.Reachable("a node restored from a snapshot")
sim.Unreachable("two leaders in the same term", term)
```

```
property       seeds reached  seeds true
  sometimes             100          99  a crash interrupted the workload
  sometimes             100          40  recovery found a torn tail

ok  100 seeds passed (0 hit the time horizon) in 32.84s
```

`always` and `unreachable` fail the seed immediately. A `sometimes` that is never true in any seed fails the sweep, because it means your faults never reached that path. Extra arguments are details: they appear in the failure message but do not split the property.

### Checking determinism

`seedfall run -check` runs every seed twice and fails with the first differing trace line if the runs diverge. Use it in CI to make sure nothing in your program escapes the simulator.

### Config reference

| Field | Meaning |
|---|---|
| `nodes[]` | Crashable nodes: `name`, `pkg` (built for wasip1 automatically) or `wasm`, `args`, `env` |
| `workload` | The checker. It is never crashed, and the run passes when it exits 0 |
| `duration` | Virtual-time horizon |
| `latency` | Per-chunk network delay range |
| `faults.crash_per_sec` | Rate of idle crashes, per node |
| `faults.restart_after` | Downtime range after a crash |
| `faults.partition_per_sec`, `partition_for` | Partition rate and duration |
| `faults.torn_writes` | Crashes keep a random prefix of unsynced writes |
| `faults.crash_on_io` | Probability of crashing during each disk write or sync |
| `faults.strict_dirs` | Directory entries survive a crash only after the parent directory is fsynced |
| `watchdog` | Real-time limit per run |

## Examples

### `examples/kvlog`: the fix that wasn't

A log-structured KV store with three durability settings:

| Config | Result |
|---|---|
| `seedfall.json` (`-sync batch`, acks before fsync) | fails on seed 1 |
| `seedfall.always.json` (fsync before every ack) | **still fails** on seed 1. A crash mid-append leaves a torn tail, recovery doesn't truncate it, and the next record is glued onto the garbage. Shrinks from 33 faults to 2. |
| `seedfall.fixed.json` (fsync + truncate the torn tail on recovery) | passes 3,000 seeds |

### `examples/raftkv`: hashicorp/raft v1.8

The real [hashicorp/raft](https://github.com/hashicorp/raft) runs as a 3-node cluster, with its own `NetworkTransport` over a `sim` stream layer and a file-backed log store. The workload writes 150 keys through the leader, then reads each acknowledged key back from the leader.

| Config | Result |
|---|---|
| `seedfall.json` | passes 600 of 600 seeds under crashes, mid-I/O crashes, torn writes and partitions |
| `seedfall.nofsync.json` (log store skips fsync) | fails on seed 9 with a committed write lost. Shrinks from 75 faults to 3: the leader n1 crashes in the middle of a disk write, then n2 and n3 crash, and none of them had synced the entry, so no copy of the committed write survives. |
| `seedfall.lin.json` (4 concurrent clients, Porcupine check) | passes 60 of 60 seeds, about 180 operations each |
| `nofsync.lin.json` | fails on seed 26: the history is not linearizable |
| `strict.lin.json` (strict POSIX directories) | **fails on seed 7. This is a real finding in hashicorp/raft (below).** |

### Finding: hashicorp/raft snapshot directory is never fsynced

`FileSnapshotSink` fsyncs `state.bin` and `meta.json`, renames the snapshot directory into place, and fsyncs the parent, but it never fsyncs the snapshot directory itself. Under strict POSIX semantics, a crash after the snapshot leaves the renamed directory **empty**. Raft has already compacted its log, so on restart `NewRaft` panics with `log not found` every time. Opening and syncing the directory before the rename fixes it: with that patch, seed 7 and seeds 1–120 pass. On ext4/XFS/btrfs a file fsync in practice also persists its directory entry, so typical Linux deployments are probably unaffected. Details and repro: [docs/findings/hashicorp-raft-snapshot-dir-fsync.md](docs/findings/hashicorp-raft-snapshot-dir-fsync.md).

## Limitations

- Code that calls `net.Dial` / `net.Listen` directly needs the one-line `sim` change. Programs using cgo, or dependencies that need OS-specific syscalls (mmap, flock, for example bbolt), don't build for wasip1.
- fsync errors (EIO) and reordering of writes *within* the pending set aren't modeled. A torn crash keeps an in-order prefix.
- Within a node, the explored interleavings are the ones the Go runtime picks under a seeded random source. seedfall does not do systematic preemption.
- Simulation is about as fast as real time for heavy guests. hashicorp/raft runs at roughly 1–2× real time per node, so a sweep's speed comes from running seeds in parallel.

## Roadmap

- fsync EIO, message duplication, per-node clock skew
- A GitHub Action
- An HTML timeline viewer for traces

## License

Apache-2.0
