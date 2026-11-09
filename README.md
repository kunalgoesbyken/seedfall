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

- **`seedfall init`** inspects a module with `go list`, reports every package that will not build for `wasip1` (compile errors and cgo) with the import chain to it, and scaffolds `seedfall.json`, with `strict`, `resilient` and `everything` profiles, and a workload.
- **Four skills**: `seedfall-setup`, `seedfall-properties` (write checks, then grade them with `mutate`), `seedfall-triage` (shrink, cause, inspect, then a counterfactual replay) and `seedfall-fix-and-prove` (finish with `verify` and paste the proof into the PR).
- **MCP**: every command as a tool, the skills as prompts, and the config schema and these docs as resources.
- **[llms.txt](https://seedfall-lemon.vercel.app/llms.txt)** and [llms-full.txt](https://seedfall-lemon.vercel.app/llms-full.txt) for models that read docs directly.

Details: [docs/agents.md](docs/agents.md).

## Bugs found

| Project | What seedfall found | Upstream (2026-11-09) |
|---|---|---|
| **NATS JetStream** | A crash while a stream is being created leaves its config without a checksum. The stream is silently re-created, accepts and acknowledges publishes, and vanishes with all of them on the next restart. 2 faults, `SyncAlways` on, any filesystem. | [nats-server#8872](https://github.com/nats-io/nats-server/issues/8872), open |
| **rosedblabs/wal** | After one crash mid-write, new fsynced writes are appended after the torn chunk and are lost to replay. rosedb v2 can no longer open. 2 faults. | [#51](https://github.com/rosedblabs/wal/issues/51), open |
| **tidwall/buntdb** | The automatic AOF shrink renames the rewritten file without fsyncing it. A crash after a shrink can bring the database back empty, even with `SyncPolicy: Always`. | [#126](https://github.com/tidwall/buntdb/issues/126), fixed in [6a23821](https://github.com/tidwall/buntdb/commit/6a23821fdfd51ab6e375b8f82407b6589cad907f) |
| **tidwall/wal** | A crash mid-write makes the log permanently unopenable (known #21), and the pending fix #22 is incomplete. 1 fault. | [#43](https://github.com/tidwall/wal/issues/43), open |
| **hashicorp/raft** | The snapshot directory is never fsynced before rename. Under strict POSIX, one crash leaves a node that panics on every restart. | [#744](https://github.com/hashicorp/raft/issues/744), open |
| **nutsdb** | `bucket.Meta` is never fsynced, so one crash can make recovery drop every acknowledged entry in a bucket. A torn tail also makes `Open` fail with `crc error` forever. 3 issues, 1 fault each. | [#695](https://github.com/nutsdb/nutsdb/issues/695), [#696](https://github.com/nutsdb/nutsdb/issues/696), [#697](https://github.com/nutsdb/nutsdb/issues/697), open |
| **bitcask** | `Open` rewrites `config.json` without fsync, and auto-recovery fails on the torn tails it is meant to repair. Either leaves the database unopenable after one crash. 4 issues. | no link recorded |
| **diskv** | A synced `WriteStream` renames into place without fsyncing the directory, so under strict POSIX an acknowledged write can revert. | [#83](https://github.com/peterbourgon/diskv/issues/83), open |
| **bbolt** | `Open` never fsyncs the directory of the file it creates, so under strict POSIX one crash can lose the whole database. Found by running bbolt unmodified through `compat_overlay`. Not filed yet. | not filed yet |
| **jmsadair/raft** | Four ways one crash or one clock step back leaves a node that can never restart: an unsynced term/vote file, an untruncated torn log tail, a leftover temp snapshot directory, and snapshots ordered by wall clock. Not filed: the repository is no longer public. | not filed: repository no longer public |

**AI-written storage.** We asked Claude for a crash-safe key-value store nine times, with one prompt and fresh sessions; every sample's own tests passed. seedfall found two that can refuse to open forever after a specific crash: one after a failed fsync during recovery, one after out-of-order page writeback. The other seven pass 260 seeds each. [examples/aikv](examples/aikv)

hashicorp/raft (600 seeds) and etcd-io/raft (100 seeds) pass cleanly, with linearizability checked, and their negative controls are caught. Details, replay commands and verified fixes: [docs/findings](docs/findings/README.md).

## Quickstart

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
seedfall doctor   # checks Go, wasip1 builds, net_overlay, caches and memory, and prints fixes
```

Homebrew, Scoop, an install script and a Docker image are set up for releases from the automated pipeline; v0.2.0 predates it, so use `go install` until a newer release is out. Paths, signatures and checksums: [Install](docs/install.md).

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
seedfall shrink -seed 9                # minimal schedule (faults, duration, workload size), saved to .seedfall/
seedfall cause  -seed 9                # probability of failing over time, and the fault that caused it
seedfall explore -seeds 1-32           # branch from runs that reached something new, to find failures a sweep misses
seedfall inspect -seed 9               # every node's disk at the failure: durable, pending and torn bytes
seedfall replay -seed 9 -html t.html   # space-time diagram of nodes, messages and faults, with a trace explorer
seedfall logs   -seed 9 -node server -grep fsync -from 1s -net-trace   # filtered trace, slog JSON parsed with -json
seedfall debug  -seed 9 -at 1.7s       # stop there: nodes, live disks, messages in flight, step, until, branch
seedfall trace grep -seed 9 -e 'crash|fsync' -context 3   # regex over the trace, with the lines around each match
seedfall trace diff a.txt b.txt        # first line where two traces part
seedfall faults -seed 9 -remove 2 -out h.json   # edit a schedule to test a hypothesis, checked against the config
seedfall check  -history .seedfall/seed-9.history.jsonl -model queue   # re-check a recorded history
seedfall run    -seeds 1-500 -check    # run every seed twice, fail on any divergence
seedfall pin    -seed 9                # keep seed 9's schedule as a regression replayed before every run
seedfall run    -seeds 1-500 -report .seedfall/report.html   # triage report of every failure
seedfall findings -status new,regressed   # read the report's findings and run history back
seedfall run    -seeds 1-500 -json     # one JSON object per seed, then a summary
seedfall mutate -seeds 1-100           # would your checks notice fsync silently breaking?
seedfall verify -seed 9 -faults .seedfall/seed-9.faults.json -md proof.md   # prove the fix
```

The [getting started guide](docs/getting-started.md) walks through finding, shrinking and fixing a real bug in five minutes.

## Features

- **Stock toolchain.** No forked runtime, and without the opt-in overlays no `linkname` and no source rewriting. Works on Linux, macOS and Windows. [How it works](docs/how-it-works.md)
- **A real disk model.** Plain `os.File` with fsync durability, torn writes, out-of-order page writeback, crashes in the middle of a disk operation, fsync `EIO` with Linux "fsyncgate" semantics, and strict POSIX directory durability. [Faults](docs/faults.md)
- **A disk that misbehaves.** A full disk that returns `ENOSPC`, read and write `EIO`, corrupted reads, lost and misdirected writes that fsync reports as done, bits flipped by a crash or by bit rot, and slow fsyncs. Every one replays and shrinks, and `seedfall inspect` shows the corrupted ranges. [Disk faults](docs/faults.md#disk-faults)
- **A disk you can trust.** Hard links with link counts, symbolic links, `Lstat`, stored `Chtimes`, and three crash models for `reorder_writes`: `ext4_ordered`, an aggressive `page_independent`, and `dir_reorder` for ext2 and btrfs, grounded in the ALICE measurements. [File system models](docs/faults.md#file-system-models)
- **Storage engines that need `mmap` or `flock`.** bbolt, pebble and badger run unmodified via `compat_overlay`, and a generic shim covers other callers of `Mmap`, `Msync` and `Flock`. Shared mappings stay coherent with `pwrite`, and `mmap_early_writeback` finds code that assumes an unsynced mapped page stays volatile. `seedfall init` names the shim for each dependency. [Compat](docs/compat.md)
- **A real network.** Partitions in Jepsen's topologies (half, one, majority, majorities ring, random groups), one-way clogs that stop, jam or slow a link, FoundationDB-style swizzles, connection resets without a crash, per-link latency, through `sim` or with zero code changes via `net_overlay`, including `net/http` and hashicorp/raft's stock TCP transport. UDP sockets with seeded latency and reordering, loss and duplication faults, port 0 listens, half-close and real peer addresses, so gossip protocols such as hashicorp/memberlist run unmodified. [Networking](docs/networking.md), [network faults](docs/faults.md#network-faults)
- **One config, many variants.** `extends` builds a config on another, `profiles` keeps every variant in one `seedfall.json` (`-profile nofsync`), `-set faults.crash_per_sec=2` overrides one value, and fault presets from `none` to `everything` replace hand-tuned rates. The profile follows a failure into its replay command, artifacts and pinned regressions. [Config](docs/config.md)
- **Seeded disks and upgrades.** Start a node from an existing disk image or host fixture files, durable from t=0, including a failing state exported by `seedfall inspect -out`, and swap in a new version of its program on restart to test upgrades against real old data. [Seeded disks](docs/config.md#seeded-disks-and-files), [examples/upgrade](examples/upgrade)
- **Real processes.** Every node is its own wasm instance. A crash kills the process; a restart boots fresh over the surviving disk. `supervise` restarts nodes that exit, like systemd.
- **Goroutine interleavings.** Spinning goroutines are preempted after a counted number of function calls. `"preempt": {"strategy": "random"}` preempts at seeded points, so a check-then-act race inside one process fails on a seed and replays exactly, and `"strategy": "pct"` runs probabilistic concurrency testing: a few seeded change points per node, each recorded as a `preempt` fault that replays and shrinks, which finds bugs where one goroutine must stall while another runs a long stretch. `"loops": true` counts loop iterations too, so call-free loops are preempted and spend virtual time. [How it works](docs/how-it-works.md#preemption)
- **Pauses, slow nodes, clock skew and clock jumps.** Freeze a node like a long GC pause, slow it down like an overloaded machine, offset or jump its wall clock. These catch stale reads and lease bugs: a raft store that reads without `VerifyLeader` fails on seed 35 under pauses, and on seed 20 under slow nodes and clogs.
- **BUGGIFY.** Mark sites in your own code with `sim.Buggify("reply lost after apply")` and turn on `buggify`: each seed enables some sites and fires them at seeded points, every firing is a fault that replays and shrinks, and the sweep reports which sites were reached, enabled and fired. `sim.Random` and `sim.RandomChoice` give replayable choices. [Buggify](docs/buggify.md)
- **Swarm testing.** Per-seed random fault mixes in the style of TigerBeetle's VOPR.
- **Properties with coverage.** `Always`, `Sometimes`, `Reachable`, `Unreachable` and `Eventually`, plus numeric forms such as `AlwaysGreaterThanOrEqualTo` that report how close each one came to failing, and the grouped `AlwaysSome` and `SometimesAll`, cataloged from your source and aggregated across the sweep, so a property that never ran fails the build instead of passing silently. `sim.Event` marks moments in the trace. [Properties](docs/properties.md)
- **Liveness checks.** `"liveness": {"within": "10s"}` adds a recovery phase to every run: the faults stop, the network heals, crashed nodes restart, and a workload that does not finish in time fails as `liveness`. `sim.Eventually` asserts that a condition becomes true by a deadline, a `finally` program checks the end state after the workload with faults stopped, and `sim.QuietFor` starts a quiet period from inside the program. [Properties](docs/properties.md#liveness-recovery-and-finally-checks)
- **Faults on the client too.** `include_workload` crashes, pauses and skews the workload, so client retries and idempotency get tested: operations in flight at a client crash count as unknown, and `workloads[]` runs several clients. `"faults": false` keeps any node out of every fault. [Faults on the workload](docs/faults.md#faults-on-the-workload)
- **Linearizability and isolation checkers.** Record concurrent operations with `sim.Put`, `sim.Get` or `sim.Invoke` and pick a model in `"checker"`: key-value, compare-and-set, append-only log, FIFO queue, set or counter, each checked with Porcupine and an HTML timeline. `sim.Txn` records list-append transactions, checked Elle-style for `G0`, `G1a`, `G1b`, `G1c`, `G-single` and `G2` with the dependency cycle explained. Failing runs and every replay save the history as JSONL, `seedfall check` re-checks it under any model, and your own Porcupine model can read it. [Properties](docs/properties.md#linearizability)
- **Exact replay and smart shrinking.** Same seed, same run, byte for byte. `-check` runs every seed twice and names the first host call that differs, and every run reports a determinism hash to compare across machines. `seedfall shrink` removes faults by delta debugging, then shortens downtimes, partitions and pauses, shrinks clock jumps, narrows partition sides, moves faults earlier, trims the run's duration and, through `sim.Budget`, the workload's operation count. Candidates run in parallel with the same result at any `-j`, `-budget` caps the runs or time, and the saved schedule replays exactly. [Shrinking](docs/how-it-works.md#shrinking)
- **Feedback-guided exploration.** `seedfall explore` keeps the runs that reach a new property outcome, fault shape, buggify firing or function, and branches further runs from the moment those signals fired, so a rare state followed by another rare event no longer needs both to happen in one attempt. `run -until-novelty` stops a sweep once batches of seeds stop adding anything. Failures are ordinary schedule files, deterministic at any `-j`. [Explore](docs/explore.md), [examples/explore](examples/explore)
- **Causality analysis.** `seedfall cause` replays a failing seed to many moments, re-randomizes everything after each one, and plots the probability of failing over time. The fault where it jumps is the cause. [Cause](docs/cause.md)
- **Timelines, logs and a time-travel debugger.** Every failure gets `timeline.html`, a self-contained space-time diagram of node lives, messages, faults, recorded operations and properties beside a trace explorer with node, kind, time and regex filters, plus a Mermaid sequence diagram; the triage report embeds it and `replay -html` writes one for any seed. `-net-trace` adds every connection and message to the trace, `seedfall logs` filters it by node, pattern and virtual time with slog JSON parsed, `seedfall debug -at 1.7s` stops the run there to inspect nodes, live disks, in-flight messages and faults, step or branch, `seedfall trace grep` finds a pattern with the lines of every node around each match, and `seedfall trace diff` finds where two traces part. [Timelines](docs/timeline.md)
- **Disk inspection.** `seedfall inspect` snapshots every node's simulated disk at the failure or any virtual time: which bytes are durable, which a crash would lose, which directory entries are not durable yet, where the last crash tore a write, and which ranges a disk fault corrupted. `-out` exports the durable image for the library's own tools. [Inspect](docs/inspect.md)
- **Mutation testing.** `seedfall mutate` silently breaks fsync, directory fsync and `O_SYNC`, corrupts what fsync writes, makes renames and truncations non-atomic, drops or duplicates TCP data, or lets clock jumps move the monotonic clock, under your nodes, and checks that your sweep notices. `-app` mutates your own source instead (flipped comparisons, skipped error returns, skipped `Sync` calls), one overlay build per mutant. A surviving mutant is a blind spot in your checks. [Mutate](docs/mutate.md)
- **Triage reports.** `-report` groups every failing seed into findings, writes an HTML and JSON report, and tracks each finding across runs as new, ongoing, resolved (with 95% confidence) or regressed. [Reports](docs/reports.md)
- **Failure artifacts.** Every reported failure leaves `.seedfall/failures/<config>-seed-N/` with `report.json` (classification, seedfall version, config hash, commit), the full trace, a replayable `faults.json`, the operation history, the linearizability timeline, the run's timeline and each node's disk image, ready to attach to an issue. [Artifacts](docs/reports.md#failure-artifacts)
- **Pinned regressions.** `seedfall pin -seed N` commits a failing schedule to `seedfall-regressions/`, and `run`, `verify`, the hook and `seedfalltest` replay every pinned schedule before the sweep, so a fixed bug stays fixed. `seedfalltest.Pinned`, `Replay` and `Seeds` turn them into `go test` cases. [Pinned regressions](docs/verify.md#pinned-regressions)
- **Alerts and issues.** `seedfall report notify` posts new and regressed findings to a Slack or Discord webhook, `seedfall report issue` files one GitHub issue per finding that has no open issue, and the HTML report copies the same issue Markdown. The GitHub Action exposes both as `webhook` and `file-issues`. [Notifications](docs/reports.md#notifications-and-issues)
- **Runs Antithesis SDK code.** Programs instrumented with the Antithesis Go SDK run unchanged: `assert.Always`, `Sometimes`, `Reachable`, the numeric and grouped variants, `lifecycle.SetupComplete` and `lifecycle.SendEvent` become seedfall properties, setup and trace events. [Antithesis compatibility](docs/antithesis.md)
- **Failure classification.** Every reported failure is classed as a `bug`, `nondeterministic` (with the first diverging trace line) or `timeout`, so CI and agents block only on real bugs. [Reports](docs/reports.md#classification)
- **Proof of fix.** `seedfall verify` replays the failing schedules, sweeps enough fresh seeds for 95% confidence, checks determinism and mutants, and writes a Markdown proof block for the pull request. [Verify](docs/verify.md)
- **Sweep feedback.** A live progress line with seeds per second, failures, ETA and virtual speedup on a terminal, a `-timeout` budget for CI, and a utilization section that says whether more seeds are likely to help. [Reports](docs/reports.md#utilization)
- **AI-agent native.** `seedfall mcp` lets Claude Code, Cursor and other agents run, replay, search traces, shrink, edit fault schedules, grade checks with mutants, pin regressions and read findings on their own, and prove a fix before opening a PR. Every MCP tool has a CLI command, and `seedfall schema` prints both surfaces as JSON. [Agents](docs/agents.md)
- **CI ready.** A GitHub Action that downloads a checksummed release, runs `run`, `verify` or `mutate`, keeps the finding history between runs, rotates seeds, annotates source lines and updates one pull request comment, plus a `go test` helper and `run -json` for one JSON object per seed. [CI](docs/ci.md)
- **Tested itself.** Unit tests pin down the crash, torn-write, writeback, fsync `EIO`, disk fault, `strict_dirs`, scheduler, network, shrink and confidence models, and an end-to-end guest proves byte-identical replay. CI runs them with `-race` on Linux, macOS and Windows, plus staticcheck, govulncheck and CodeQL, and every example nightly. [Testing](docs/testing.md)
- **Gostman collections as workloads.** Run the API checks you already keep in a [Gostman](https://github.com/kunalgoesbyken/gostman) collection while seedfall crashes and partitions the service behind them. [Gostman](docs/gostman.md)

## Examples

| Example | What it shows |
|---|---|
| [`examples/kvlog`](examples/kvlog) | A log-structured store: ack-before-fsync, the torn-tail recovery bug that survives "fsync before ack", the fsyncgate hole, and their fixes, then corrupted reads fixed by checksums, a full disk, and the lost writes, misdirected writes and bit rot that one copy cannot survive |
| [`examples/symswap`](examples/symswap) | A store that publishes generations by renaming a symlink: passes under the default `ext4_ordered` model, and `dir_reorder` finds the rename surviving without the file it points at |
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
| [`examples/memberlist`](examples/memberlist) | An unmodified hashicorp/memberlist cluster gossiping over simulated UDP under loss, duplication and partitions |
| [`examples/netcompat`](examples/netcompat) | A conformance check of port 0, `CloseRead`, `CloseWrite`, peer addresses and UDP, through both `net_overlay` and `sim` |
| [`examples/mmapwal`](examples/mmapwal) | A write-ahead log over a writable mapping, through the generic shim with `blevesearch/mmap-go` and `gofrs/flock`: a checkpoint-checksum recovery that `mmap_early_writeback` proves wrong |
| [`examples/bboltkv`](examples/bboltkv) | Unmodified bbolt through `compat_overlay`, including the directory-fsync finding |
| [`examples/pebblekv`](examples/pebblekv) | Unmodified pebble v2 through `compat_overlay`, with `Sync` and `NoSync` writes |
| [`examples/badgerkv`](examples/badgerkv) | Unmodified badger v4 through `compat_overlay`, its writable `mmap` emulated |
| [`examples/checkers`](examples/checkers) | One store behind every history checker: queue, compare-and-set lock, counter, set, append-only log and list-append transactions, each passing with a lock and failing on seed 1 without it |
| [`examples/interleave`](examples/interleave) | A check-then-act race between goroutines in one process, found only with seeded preemption (`preempt.strategy` `random`), and a journal checkpoint race that `pct` finds on every seed and `random` almost never |
| [`examples/busyloop`](examples/busyloop) | Call-free loops under `preempt.loops`: a lease that expires during a checksum, and a spin-wait that fails as `liveness` instead of on the real-time watchdog |
| [`examples/explore`](examples/explore) | A contrived six-level climb that a plain sweep of 20 seeds cannot finish and `seedfall explore` finds in 32 runs by branching from each new level |
| [`examples/buggify`](examples/buggify) | A counter whose client retries a lost reply: passes without faults, and a `sim.Buggify` site that drops the reply after applying finds the double increment |
| [`examples/liveness`](examples/liveness) | A primary and backup under liveness mode with a `finally` check and the client crashing too: a primary that never reconnects fails liveness, and retries without request deduplication apply an add twice |
| [`examples/aikv`](examples/aikv) | Nine key-value stores written by Claude from one prompt, each under five fault profiles and two durability mutants |
| [`examples/upgrade`](examples/upgrade) | A ledger that changes its on-disk format: the node starts from a seeded v1 disk, restarts into v2 after its first crash, and an in-place migration that a crash leaves half done loses deposits |
| [`examples/gostman`](examples/gostman) | A [Gostman](https://github.com/kunalgoesbyken/gostman) collection as the workload: its checks must pass after every fault window |

`bash scripts/regress.sh` runs every example against its expected result; CI runs it nightly. [Testing seedfall itself](docs/testing.md) covers the unit tests and CI.

## Limitations

- Code must build for `GOOS=wasip1`, so no cgo. Dependencies that need `mmap` or `flock` work through `compat_overlay`: a shim for bbolt, pebble and badger, or the generic shim for anything whose `Mmap` or `Flock` calls are in a unix-constrained file. Files named `_linux.go` and code using other unix calls still need a hand-written shim ([compat](docs/compat.md)).
- Preemption is counted in function calls, not time: a goroutine that spins is preempted after about four million calls, and a loop that makes no function call is preempted only with `preempt.loops`. Even then the Go runtime on `wasip1` switches goroutines only at a function entry, so a call-free loop parks its node but still keeps the node's other goroutines from running until it makes a call.
- Goroutine interleavings inside a node come from the Go scheduler under a seeded random source. The `random`, `pct` and `calls` strategies add seeded or fixed preemption points ([`examples/interleave`](examples/interleave)), but they sample interleavings rather than enumerating them, `pct` picks only the change points and not the goroutine priorities, so its probability bound is a guide rather than a guarantee, and none of them detects data races the way `go test -race` does.
- `fs_model` covers ext4 `data=ordered`, an aggressive independent-pages model, and ext2 or btrfs directory reordering, not every file system: XFS, `data=journal` and `nodelalloc` have no model of their own. A single directory operation is always atomic, unlike on ext2. `Chmod` is not modelled, because WASI has no such call.
- Real time is spent on guest CPU and node boots, never on idle virtual time. A busy 3-node NATS JetStream cluster under faults simulates about 2.5-3x faster than real time and a mostly idle JetStream node about 7x, but short seeds of large guests are dominated by booting each node (0.1-0.3s per start for nats-server). Sweeps add one core per seed. See [how it works](docs/how-it-works.md#speed).

## Docs and contributing

The [docs index](docs/README.md) lists every page as tutorials, how-to guides, reference and explanation. When something fails to build or a run fails for a reason outside your system, start with [troubleshooting](docs/troubleshooting.md); the [FAQ](docs/faq.md) compares seedfall with Antithesis, gosim, `testing/synctest`, `-race` and Jepsen. [CONTRIBUTING](CONTRIBUTING.md) covers the tests, adding an example or compat shim and writing up a finding, and the [roadmap](ROADMAP.md) lists what is not done yet. Report vulnerabilities privately as described in [SECURITY](SECURITY.md).

## License

Apache-2.0
