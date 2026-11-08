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

<p align="center">
  <a href="https://github.com/kunalgoesbyken/seedfall/actions/workflows/ci.yml"><img src="https://github.com/kunalgoesbyken/seedfall/actions/workflows/ci.yml/badge.svg" alt="ci"></a>
  <a href="https://github.com/kunalgoesbyken/seedfall/actions/workflows/regress.yml"><img src="https://github.com/kunalgoesbyken/seedfall/actions/workflows/regress.yml/badge.svg" alt="regress"></a>
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

## For AI coding agents

seedfall is built so a coding agent can adopt it in an unfamiliar Go repository and prove its own changes crash-safe, with no human in the loop and no API key:

```sh
claude plugin install seedfall --marketplace kunalgoesbyken/seedfall   # Claude Code: MCP server, skills and an opt-in Stop hook
npx skills add kunalgoesbyken/seedfall                                 # the same skills for other agents
claude mcp add seedfall -- seedfall mcp                                # or only the MCP server
```

- **`seedfall init`** inspects a module with `go list`, reports every package that will not build for `wasip1` (compile errors and cgo) with the import chain to it, and scaffolds `seedfall.json` and a workload.
- **Four skills**: `seedfall-setup`, `seedfall-properties` (write checks, then grade them with `mutate`), `seedfall-triage` (shrink, cause, inspect, then a counterfactual replay) and `seedfall-fix-and-prove` (finish with `verify` and paste the proof into the PR).
- **MCP**: every command as a tool, the skills as prompts, and the config schema and these docs as resources.
- **[llms.txt](https://seedfall-lemon.vercel.app/llms.txt)** and [llms-full.txt](https://seedfall-lemon.vercel.app/llms-full.txt) for models that read docs directly.

Details: [docs/agents.md](docs/agents.md).

## Bugs found

| Project | What seedfall found |
|---|---|
| **NATS JetStream** | A crash while a stream is being created leaves its config without a checksum. The stream is silently re-created, accepts and acknowledges publishes, and vanishes with all of them on the next restart. 2 faults, `SyncAlways` on, any filesystem. |
| **rosedblabs/wal** | After one crash mid-write, new fsynced writes are appended after the torn chunk and are lost to replay. rosedb v2 can no longer open. 2 faults. |
| **tidwall/buntdb** | The automatic AOF shrink renames the rewritten file without fsyncing it. A crash after a shrink can bring the database back empty, even with `SyncPolicy: Always`. |
| **tidwall/wal** | A crash mid-write makes the log permanently unopenable (known #21), and the pending fix #22 is incomplete. 1 fault. |
| **hashicorp/raft** | The snapshot directory is never fsynced before rename. Under strict POSIX, one crash leaves a node that panics on every restart. |
| **nutsdb** | `bucket.Meta` is never fsynced, so one crash can make recovery drop every acknowledged entry in a bucket. A torn tail also makes `Open` fail with `crc error` forever. 3 issues, 1 fault each. |
| **bitcask** | `Open` rewrites `config.json` without fsync, and auto-recovery fails on the torn tails it is meant to repair. Either leaves the database unopenable after one crash. 4 issues. |
| **diskv** | A synced `WriteStream` renames into place without fsyncing the directory, so under strict POSIX an acknowledged write can revert. |
| **bbolt** | `Open` never fsyncs the directory of the file it creates, so under strict POSIX one crash can lose the whole database. Found by running bbolt unmodified through `compat_overlay`. Not filed yet. |
| **jmsadair/raft** | Four ways one crash or one clock step back leaves a node that can never restart: an unsynced term/vote file, an untruncated torn log tail, a leftover temp snapshot directory, and snapshots ordered by wall clock. Not filed: the repository is no longer public. |

**AI-written storage.** We asked Claude for a crash-safe key-value store nine times, with one prompt and fresh sessions; every sample's own tests passed. seedfall found two that can refuse to open forever after a specific crash: one after a failed fsync during recovery, one after out-of-order page writeback. The other seven pass 260 seeds each. [examples/aikv](examples/aikv)

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
seedfall inspect -seed 9               # every node's disk at the failure: durable, pending and torn bytes
seedfall run    -seeds 1-500 -check    # run every seed twice, fail on any divergence
seedfall run    -seeds 1-500 -report .seedfall/report.html   # triage report of every failure
seedfall run    -seeds 1-500 -json     # one JSON object per seed, then a summary
seedfall mutate -seeds 1-100           # would your checks notice fsync silently breaking?
seedfall verify -seed 9 -faults .seedfall/seed-9.faults.json -md proof.md   # prove the fix
```

The [getting started guide](docs/getting-started.md) walks through finding, shrinking and fixing a real bug in five minutes.

## Features

- **Stock toolchain.** No `linkname`, no source rewriting, no forked runtime. Works on Linux, macOS and Windows. [How it works](docs/how-it-works.md)
- **A real disk model.** Plain `os.File` with fsync durability, torn writes, out-of-order page writeback, crashes in the middle of a disk operation, fsync `EIO` with Linux "fsyncgate" semantics, and strict POSIX directory durability. [Faults](docs/faults.md)
- **Storage engines that need `mmap`.** bbolt, pebble and badger run unmodified via `compat_overlay`, which swaps their platform files for `wasip1` versions. [Compat](docs/compat.md)
- **A real network.** Partitions, latency, resets, through `sim` or with zero code changes via `net_overlay`, including `net/http` and hashicorp/raft's stock TCP transport. [Networking](docs/networking.md)
- **Real processes.** Every node is its own wasm instance. A crash kills the process; a restart boots fresh over the surviving disk. `supervise` restarts nodes that exit, like systemd.
- **Goroutine interleavings.** Spinning goroutines are preempted after a counted number of function calls, and `"preempt": {"interleave": true}` preempts at seeded points, so a check-then-act race inside one process fails on a seed and replays exactly. [How it works](docs/how-it-works.md#preemption)
- **Pauses, clock skew and clock jumps.** Freeze a node like a long GC pause, offset or jump its wall clock. These catch stale reads and lease bugs: a raft store that reads without `VerifyLeader` fails on seed 35.
- **BUGGIFY.** Mark sites in your own code with `sim.Buggify("reply lost after apply")` and turn on `buggify`: each seed enables some sites and fires them at seeded points, every firing is a fault that replays and shrinks, and the sweep reports which sites were reached, enabled and fired. `sim.Random` and `sim.RandomChoice` give replayable choices. [Buggify](docs/buggify.md)
- **Swarm testing.** Per-seed random fault mixes in the style of TigerBeetle's VOPR.
- **Properties with coverage.** `Always`, `Sometimes`, `Reachable` and `Unreachable`, cataloged from your source and aggregated across the sweep, so a property that never ran fails the build instead of passing silently. [Properties](docs/properties.md)
- **Liveness checks.** `sim.QuietFor` stops the faults and heals partitions so you can assert the system recovers; `sim.SetupComplete` holds faults until bootstrap is done.
- **Linearizability.** Record concurrent `sim.Put` / `sim.Get` operations and get a Porcupine check with an HTML timeline.
- **Exact replay and shrinking.** Same seed, same run, byte for byte. Shrunk schedules replay exactly.
- **Causality analysis.** `seedfall cause` replays a failing seed to many moments, re-randomizes everything after each one, and plots the probability of failing over time. The fault where it jumps is the cause. [Cause](docs/cause.md)
- **Disk inspection.** `seedfall inspect` snapshots every node's simulated disk at the failure or any virtual time: which bytes are durable, which a crash would lose, which directory entries are not durable yet, and where the last crash tore a write. `-out` exports the durable image for the library's own tools. [Inspect](docs/inspect.md)
- **Mutation testing.** `seedfall mutate` silently breaks fsync, directory fsync and `O_SYNC` under your nodes and checks that your sweep notices. A surviving mutant is a blind spot in your checks. [Mutate](docs/mutate.md)
- **Triage reports.** `-report` groups every failing seed into findings, writes an HTML and JSON report, and tracks each finding across runs as new, ongoing, resolved (with 95% confidence) or regressed. [Reports](docs/reports.md)
- **Runs Antithesis SDK code.** Programs instrumented with the Antithesis Go SDK run unchanged: `assert.Always`, `Sometimes`, `Reachable` and `lifecycle.SetupComplete` become seedfall properties and setup. [Antithesis compatibility](docs/antithesis.md)
- **Failure classification.** Every reported failure is classed as a `bug`, `nondeterministic` (with the first diverging trace line) or `timeout`, so CI and agents block only on real bugs. [Reports](docs/reports.md#classification)
- **Proof of fix.** `seedfall verify` replays the failing schedules, sweeps enough fresh seeds for 95% confidence, checks determinism and mutants, and writes a Markdown proof block for the pull request. [Verify](docs/verify.md)
- **AI-agent native.** `seedfall mcp` lets Claude Code, Cursor and other agents run, replay and shrink on their own, and prove a fix before opening a PR. [Agents](docs/agents.md)
- **CI ready.** A GitHub Action, a `go test` helper, and `run -json` for one JSON object per seed. [CI](docs/ci.md)
- **Tested itself.** Unit tests pin down the crash, torn-write, writeback, fsync `EIO`, `strict_dirs`, scheduler, network, shrink and confidence models, and an end-to-end guest proves byte-identical replay. CI runs them with `-race` on Linux, macOS and Windows, plus staticcheck, govulncheck and CodeQL, and every example nightly. [Testing](docs/testing.md)
- **Gostman collections as workloads.** Run the API checks you already keep in a [Gostman](https://github.com/kunalgoesbyken/gostman) collection while seedfall crashes and partitions the service behind them. [Gostman](docs/gostman.md)

## Examples

| Example | What it shows |
|---|---|
| [`examples/kvlog`](examples/kvlog) | A log-structured store: ack-before-fsync, the torn-tail recovery bug that survives "fsync before ack", the fsyncgate hole, and their fixes |
| [`examples/metapage`](examples/metapage) | A store that commits records through a meta page: passes under torn writes, and `reorder_writes` finds the meta page reaching the disk before the record it commits |
| [`examples/raftkv`](examples/raftkv) | hashicorp/raft v1.8 with linearizability checking, over a `sim` stream layer or the stock TCP transport |
| [`examples/etcdraft`](examples/etcdraft) | etcd-io/raft v3 with a raftexample-style WAL and snapshots, linearizability checked |
| [`examples/wals`](examples/wals) | tidwall/wal and rosedblabs/wal behind one log server and client |
| [`examples/natsjs`](examples/natsjs) | An embedded nats-server with JetStream file storage, piped over the simulated network |
| [`examples/buntdb`](examples/buntdb) | tidwall/buntdb, including the AOF shrink finding |
| [`examples/nutsdb`](examples/nutsdb) | A key-value server on nutsdb, with the `bucket.Meta`, torn-tail, rotation and fsync-error findings |
| [`examples/bitcask`](examples/bitcask) | bitcask v2, with the `config.json`, auto-recovery, rotation and fsync-error findings |
| [`examples/diskv`](examples/diskv) | peterbourgon/diskv with `TempDir` and synced writes, including the directory-fsync finding |
| [`examples/jmsraft`](examples/jmsraft) | jmsadair/raft v0.2.0 with its own storage, linearizability checked, and its stock gRPC transport available under `net_overlay` |
| [`examples/natscluster`](examples/natscluster) | A 3-node nats-server cluster with an R3 JetStream stream, run unmodified through `net_overlay` |
| [`examples/ledger`](examples/ledger) | A service with no seedfall imports, checked only with the Antithesis SDK's assertions |
| [`examples/httpkv`](examples/httpkv) | A plain `net/http` server with no seedfall code, run through `net_overlay` |
| [`examples/bboltkv`](examples/bboltkv) | Unmodified bbolt through `compat_overlay`, including the directory-fsync finding |
| [`examples/pebblekv`](examples/pebblekv) | Unmodified pebble v2 through `compat_overlay`, with `Sync` and `NoSync` writes |
| [`examples/badgerkv`](examples/badgerkv) | Unmodified badger v4 through `compat_overlay`, its writable `mmap` emulated |
| [`examples/interleave`](examples/interleave) | A check-then-act race between goroutines in one process, found only with seeded preemption (`preempt.interleave`) |
| [`examples/buggify`](examples/buggify) | A counter whose client retries a lost reply: passes without faults, and a `sim.Buggify` site that drops the reply after applying finds the double increment |
| [`examples/aikv`](examples/aikv) | Nine key-value stores written by Claude from one prompt, each under five fault profiles and two durability mutants |
| [`examples/gostman`](examples/gostman) | A [Gostman](https://github.com/kunalgoesbyken/gostman) collection as the workload: its checks must pass after every fault window |

`bash scripts/regress.sh` runs every example against its expected result; CI runs it nightly. [Testing seedfall itself](docs/testing.md) covers the unit tests and CI.

## Limitations

- Code must build for `GOOS=wasip1`, so no cgo. Dependencies that need `mmap` or `flock` work only if seedfall ships a shim for them: bbolt, pebble and badger do, through `compat_overlay` ([compat](docs/compat.md)).
- Preemption is counted in function calls, not time: a goroutine that spins is preempted after about four million calls, and a loop that makes no function call at all is never preempted.
- Goroutine interleavings inside a node come from the Go scheduler under a seeded random source. `"preempt": {"interleave": true}` adds seeded preemption points between function calls, which finds check-then-act races between goroutines ([`examples/interleave`](examples/interleave)), but it samples interleavings rather than enumerating them and does not detect data races the way `go test -race` does.
- `reorder_writes` models the page cache as independent 4 KiB pages, which is weaker than ext4 `data=ordered` for appends: it can persist a later appended page before an earlier one, which ext4 does not. Directory operations under `strict_dirs` still persist as an in-order prefix, as on journaling file systems; ext2 and btrfs can reorder them, which is not modeled.
- Real time is spent on guest CPU and node boots, never on idle virtual time. A busy 3-node NATS JetStream cluster under faults simulates about 2.5-3x faster than real time and a mostly idle JetStream node about 7x, but short seeds of large guests are dominated by booting each node (0.1-0.3s per start for nats-server). Sweeps add one core per seed. See [how it works](docs/how-it-works.md#speed).

## License

Apache-2.0
