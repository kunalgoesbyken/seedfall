# seedfall: build plan

## The problem
Stateful Go systems (databases, queues, consensus, replication, durable workflow engines) lose acknowledged data because of rare combinations of crashes, partial disk writes, lost fsyncs, network partitions and goroutine timing. These bugs hardly ever show up in tests and can't be reproduced when they do.

Evidence (research 2026-11-07, details in `research_notes/project_idea_research_2026-11-07.md`):
- Jepsen, NATS JetStream 2.12.1 (Dec 2025): 679,153 of 1,367,069 acknowledged writes lost (about 49.7%) after file corruption on a minority of nodes. NATS fsyncs every 2 minutes but acknowledges immediately. Several of these issues are still open (#7549, #7556, #7567).
- etcd 3.5.0–3.5.5: a crash during defrag caused member divergence that went unnoticed for 5 patch releases.
- golang/go#80316 (Jul 2026) asked for deterministic concurrency testing and was closed as not planned. golang/go#67690 (seeded runtime randomness) is still open. `testing/synctest` fakes time only: it doesn't control interleavings and has no network or disk faults.
- Antithesis is the commercial answer, with a $17,520/yr published entry commitment. Polar Signals said "pricing just didn't work for us" and built a private wasm harness that found 3 data-loss and 2 duplication bugs in about 2 weeks.

## The gap
About 10 Go DST projects exist, all with ≤4 stars and created between 2025 and 2026. Each one needs one of the following:
| Approach | Projects | Cost |
|---|---|---|
| Source rewriting plus runtime linkname | gosim (dead since 2024, breaks on Go ≥1.24), neria-cloud/gosim, detsim | Breaks on each Go release; needs `-checklinkname=0`; no Windows |
| Code must take injected net/fs | faultline, lockstep, simtest-go | You must refactor the system under test |
| Forked Go toolchain | polarsignals/go, godst, pont | Unmaintained forks |

None of them simulates several unmodified processes on the stock toolchain on every OS.

## The approach: stock Go → wasip1 → wazero
On `GOOS=wasip1` the Go runtime is single-threaded with no async preemption. All of its nondeterminism goes through three WASI calls: `random_get`, `clock_time_get` and `poll_oneoff`. seedfall implements them in the host:
- `random_get` reads a ChaCha8 stream derived from the run seed, which also fixes map order, `select` choice and math/rand.
- The clocks read a virtual clock.
- `poll_oneoff`/`Nanosleep` is where a node parks. The scheduler then picks the next node (ties broken by the seed) and jumps virtual time forward.

This was verified on Go 1.27.1 on Windows on 2026-11-07: the same seed gives byte-identical output, a different seed gives a different interleaving, and a 10 s sleep costs 0 real time.

Each node is a separate wazero module instance, so it has real process isolation. Crashing a node kills the instance and drops its unsynced disk state. A restart instantiates the module again over the surviving disk.

| Layer | Simulated by | Code changes needed |
|---|---|---|
| Time, randomness, scheduling | WASI host calls | none |
| Disk (`os.File`, `fsync`) | in-memory `sys.FS` with a durability model: crash keeps only synced data, optionally a torn prefix of pending writes | none |
| Network | `seedfall/sim.Listen`/`sim.Dial` returning standard `net.Listener`/`net.Conn`; falls back to real TCP outside the simulator | one line where the listener/dialer is created |
| Assertions | `sim.Fail`, or any panic / non-zero exit | optional |

## Built (2026-11-07)
- [x] Determinism spike (Go 1.27.1, Windows)
- [x] Engine: seeded scheduler, virtual clock, one wazero instance per node incarnation, real-time watchdog, deadlock detection
- [x] Event-driven network: connections are WASI fds registered with Go's netpoller; a custom `poll_oneoff` parks a node until its next timer or packet arrival (46k → 6k scheduler steps on raft)
- [x] Simulated disk: fsync durability, crash rollback, torn writes, crash during the N-th disk op (`crash_on_io`)
- [x] Strict POSIX directory durability (`strict_dirs`), with entries filtered by ancestor reachability on recovery
- [x] Fault schedule from the seed (crash/restart, partition/heal, crash_io), replayable from a file
- [x] Linearizability: `sim.Put`/`sim.Get` histories checked with Porcupine, HTML timeline on violation
- [x] CLI: `run` (parallel sweep, lowest failing seed), `replay`, `shrink` (greedy fault minimization)
- [x] Examples: kvlog (two durability bugs plus the fix), raftkv (real hashicorp/raft v1.8, safe and buggy variants, concurrent linearizability workload)
- [x] Finding: hashicorp/raft never fsyncs the snapshot directory before renaming it (docs/findings), fix verified locally
- [x] Landing page in `site/`, modeled on the Antithesis/TigerBeetle design language
- [x] Regression suite: `scripts/regress.sh`

## Next
- File the hashicorp/raft issue (draft in docs/findings) after user review
- fsync EIO, message duplication, per-node clock skew
- `seedfall test ./...` integration and a GitHub Action
- HTML trace viewer; faster sweeps (instance pooling)

## Risks
- Programs that dial with `net.Dial` directly need the one-line shim. cgo and the race detector aren't available under wasm.
- Dependencies that use OS-specific syscalls (mmap, flock) won't build for wasip1. bbolt is an example.
- Simulated fsync semantics are a model, not ext4. They are documented explicitly.
