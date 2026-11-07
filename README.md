<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img src="docs/assets/logo-light.svg" alt="seedfall" width="300">
  </picture>
</p>

<p align="center">
  <b>Deterministic simulation testing for Go.</b><br>
  Crash your services mid-write, tear their writes, fail their fsyncs and partition their network.<br>
  Replay any failure from one seed.
</p>

<p align="center">
  <a href="https://seedfall-lemon.vercel.app">Website</a> ·
  <a href="docs/getting-started.md">Getting started</a> ·
  <a href="docs/findings/README.md">Bugs found</a> ·
  <a href="docs/agents.md">AI agents (MCP)</a> ·
  <a href="https://pkg.go.dev/github.com/kunalgoesbyken/seedfall">Go reference</a>
</p>

---

seedfall compiles your unmodified Go services for `wasip1`, runs a whole cluster on one simulated clock, and owns every source of nondeterminism: time, randomness, goroutine scheduling, disk and network. When an invariant breaks you get the seed, the exact fault schedule and a full trace, and `seedfall shrink` cuts the schedule down to the few faults that matter.

```
$ seedfall run -c rosedb.json -seeds 1-500
FAIL seed 1 at t=3.12130224s (867 scheduler steps, 382ms real)
  client#1: acknowledged entry 27 lost: server now answers "VAL e141-1111111111111111111111"

$ seedfall shrink -c rosedb.json -seed 1
faults before the failure (2):
  427.268ms  crash server during disk op #56, restart at 791.388ms
   1.7052s  crash server, restart at 1.942589s
```

## Bugs found

| Project | What seedfall found |
|---|---|
| **NATS JetStream** | A crash while a stream is being created leaves its config without a checksum. The stream is silently re-created, accepts and acknowledges publishes, and vanishes with all of them on the next restart. 2 faults, `SyncAlways` on, any filesystem. |
| **rosedblabs/wal** | After one crash mid-write, new fsynced writes are appended after the torn chunk and are lost to replay. rosedb v2 can no longer open. 2 faults. |
| **tidwall/buntdb** | The automatic AOF shrink renames the rewritten file without fsyncing it. A crash after a shrink can bring the database back empty, even with `SyncPolicy: Always`. |
| **tidwall/wal** | A crash mid-write makes the log permanently unopenable (known #21), and the pending fix #22 is incomplete. 1 fault. |
| **hashicorp/raft** | The snapshot directory is never fsynced before rename. Under strict POSIX, one crash leaves a node that panics on every restart. |

hashicorp/raft (600 seeds) and etcd-io/raft (100 seeds) pass cleanly, with linearizability checked, and their negative controls are caught. Details, replay commands and verified fixes: [docs/findings](docs/findings/README.md).

## Quickstart

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
```

Make nodes reachable on the simulated network, either with one line or with no code changes:

```go
ln, err := sim.Listen("tcp", ":7000")       // falls back to net.Listen outside the simulator
conn, err := sim.Dial("tcp", "server:7000")
```

```json
{ "net_overlay": true }
```

Describe the world in `seedfall.json`:

```json
{
  "nodes": [{ "name": "server", "pkg": "./server" }],
  "workload": { "name": "client", "pkg": "./client" },
  "faults": {
    "crash_per_sec": 0.5,
    "torn_writes": true,
    "crash_on_io": 0.005,
    "fsync_eio": 0.01,
    "partition_per_sec": 0.2
  }
}
```

The workload is any Go program that drives your system and checks invariants:

```go
sim.Assert(got == want, "acknowledged write lost: "+key)
sim.Sometimes(tornTail, "recovery found a torn tail")
```

Then sweep, replay and shrink:

```sh
seedfall run    -seeds 1-5000          # parallel sweep, reports the lowest failing seed
seedfall replay -seed 9                # exact replay with the full trace
seedfall shrink -seed 9                # minimal fault schedule, saved to .seedfall/
seedfall cause  -seed 9                # probability of failing over time, and the fault that caused it
seedfall run    -seeds 1-500 -check    # run every seed twice, fail on any divergence
seedfall run    -seeds 1-500 -report .seedfall/report.html   # triage report of every failure
seedfall mutate -seeds 1-100           # would your checks notice fsync silently breaking?
```

The [getting started guide](docs/getting-started.md) walks through finding, shrinking and fixing a real bug in five minutes.

## Features

- **Stock toolchain.** No `linkname`, no source rewriting, no forked runtime. Works on Linux, macOS and Windows. [How it works](docs/how-it-works.md)
- **A real disk model.** Plain `os.File` with fsync durability, torn writes, crashes in the middle of a disk operation, fsync `EIO` with Linux "fsyncgate" semantics, and strict POSIX directory durability. [Faults](docs/faults.md)
- **A real network.** Partitions, latency, resets, through `sim` or with zero code changes via `net_overlay`, including `net/http` and hashicorp/raft's stock TCP transport. [Networking](docs/networking.md)
- **Real processes.** Every node is its own wasm instance. A crash kills the process; a restart boots fresh over the surviving disk. `supervise` restarts nodes that exit, like systemd.
- **Pauses, clock skew and clock jumps.** Freeze a node like a long GC pause, offset or jump its wall clock. These catch stale reads and lease bugs: a raft store that reads without `VerifyLeader` fails on seed 13.
- **Swarm testing.** Per-seed random fault mixes in the style of TigerBeetle's VOPR.
- **Properties with coverage.** `Always`, `Sometimes`, `Reachable` and `Unreachable`, cataloged from your source and aggregated across the sweep, so a property that never ran fails the build instead of passing silently. [Properties](docs/properties.md)
- **Liveness checks.** `sim.QuietFor` stops the faults and heals partitions so you can assert the system recovers; `sim.SetupComplete` holds faults until bootstrap is done.
- **Linearizability.** Record concurrent `sim.Put` / `sim.Get` operations and get a Porcupine check with an HTML timeline.
- **Exact replay and shrinking.** Same seed, same run, byte for byte. Shrunk schedules replay exactly.
- **Causality analysis.** `seedfall cause` replays a failing seed to many moments, re-randomizes everything after each one, and plots the probability of failing over time. The fault where it jumps is the cause. [Cause](docs/cause.md)
- **Mutation testing.** `seedfall mutate` silently breaks fsync, directory fsync and `O_SYNC` under your nodes and checks that your sweep notices. A surviving mutant is a blind spot in your checks. [Mutate](docs/mutate.md)
- **Triage reports.** `-report` groups every failing seed into findings, writes an HTML and JSON report, and tracks each finding across runs as new, ongoing, resolved (with 95% confidence) or regressed. [Reports](docs/reports.md)
- **Runs Antithesis SDK code.** Programs instrumented with the Antithesis Go SDK run unchanged: `assert.Always`, `Sometimes`, `Reachable` and `lifecycle.SetupComplete` become seedfall properties and setup. [Antithesis compatibility](docs/antithesis.md)
- **AI-agent native.** `seedfall mcp` lets Claude Code, Cursor and other agents run, replay and shrink on their own, and prove a fix before opening a PR. [Agents](docs/agents.md)
- **CI ready.** A GitHub Action and a `go test` helper. [CI](docs/ci.md)

## Examples

| Example | What it shows |
|---|---|
| [`examples/kvlog`](examples/kvlog) | A log-structured store: ack-before-fsync, the torn-tail recovery bug that survives "fsync before ack", the fsyncgate hole, and their fixes |
| [`examples/raftkv`](examples/raftkv) | hashicorp/raft v1.8 with linearizability checking, over a `sim` stream layer or the stock TCP transport |
| [`examples/etcdraft`](examples/etcdraft) | etcd-io/raft v3 with a raftexample-style WAL and snapshots, linearizability checked |
| [`examples/wals`](examples/wals) | tidwall/wal and rosedblabs/wal behind one log server and client |
| [`examples/natsjs`](examples/natsjs) | An embedded nats-server with JetStream file storage, piped over the simulated network |
| [`examples/buntdb`](examples/buntdb) | tidwall/buntdb, including the AOF shrink finding |
| [`examples/ledger`](examples/ledger) | A service with no seedfall imports, checked only with the Antithesis SDK's assertions |
| [`examples/httpkv`](examples/httpkv) | A plain `net/http` server with no seedfall code, run through `net_overlay` |

`bash scripts/regress.sh` runs every example against its expected result.

## Limitations

- Code must build for `GOOS=wasip1`: no cgo, and no dependencies that need `mmap` or `flock` (for example bbolt or pebble).
- Goroutine interleavings inside a node come from the Go scheduler under a seeded random source. There is no systematic preemption, so use `go test -race` for shared-memory races. seedfall targets distributed and storage bugs.
- Reordering of writes within the pending set is not modeled. A torn crash keeps an in-order prefix.
- Heavy guests run at roughly real-time speed; sweeps get their speed from running seeds in parallel.

## License

Apache-2.0
