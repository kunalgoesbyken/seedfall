# FAQ

Short answers to the questions that come up most, with links to the pages that go into detail.

## How is seedfall different from Antithesis?

Antithesis runs arbitrary containers inside a deterministic hypervisor, so it can test software in any language with its real operating system underneath. seedfall runs only Go programs that build for `wasip1`, inside one process, with a simulated disk and network. In exchange it runs locally and in CI with no service and no account, and it is open source.

Programs instrumented with the Antithesis Go SDK run under seedfall unchanged: `assert.Always`, `Sometimes`, `Reachable`, the numeric and grouped variants, `lifecycle.SetupComplete` and `lifecycle.SendEvent` become seedfall properties, setup and trace events. `seedfall cause` uses the same idea as Antithesis's multiverse debugging, with exact replay from the start standing in for VM snapshots. See [Antithesis compatibility](antithesis.md).

## How is it different from gosim?

[gosim](https://github.com/jellevandenhooff/gosim) is also deterministic simulation testing for Go, with simulated machines, a network, a file system and crashes. It transforms your program and its dependencies to run on its own simulated runtime, at the system call level. seedfall does not transform code: it compiles the program with the stock Go toolchain for `GOOS=wasip1` and owns nondeterminism at the WASI boundary, where time, randomness and blocking leave the guest, plus its own file system and network calls. Each node is a separate wasm instance, so a crash kills the whole process and a restart boots a fresh one over the surviving disk. Check gosim's own documentation for its current feature set rather than relying on this page.

## Why not `testing/synctest`?

`testing/synctest` runs a test's goroutines in a bubble with a fake clock that advances when every goroutine in the bubble is blocked. It makes time-dependent unit tests fast and repeatable inside one `go test` process. It does not simulate a disk or a network, crash or restart processes, inject faults, or record a schedule you can replay and shrink, and goroutine interleavings still come from the real scheduler.

seedfall is for whole services: several nodes as separate processes on one virtual clock, with crashes, torn writes, fsync errors, partitions and clock faults, and any failure replayed from its seed. The two fit together: `synctest` for a component's timing logic, seedfall for what the service does when the machine fails under it.

## Does seedfall replace `go test -race`?

No. On `wasip1` the Go runtime is single-threaded, so goroutines never run in parallel and the memory accesses that the race detector looks for never overlap. The `random`, `pct` and `calls` preemption strategies find interleaving bugs such as a check-then-act race between goroutines, but they do not detect data races the way `-race` does. Keep running `-race` on your unit tests; seedfall's own CI does.

## How does it compare with Jepsen?

Jepsen tests real deployments of a database on real or virtual machines, in any language, with faults injected into the real operating system and network, and checks the recorded history for consistency anomalies. It finds bugs in the system as shipped, but a failing run cannot be replayed exactly.

seedfall borrows from Jepsen: the partition topologies follow Jepsen's nemeses, histories are recorded in Jepsen's invoke and completion shape, and `sim.Txn` histories are checked for the same Elle anomalies (`G0`, `G1a`, `G1b`, `G1c`, `G-single`, `G2`). The difference is that every run is deterministic, so a failure comes with a seed, an exact fault schedule and a trace, and `seedfall shrink` cuts the schedule down to the faults that matter. The cost is that seedfall tests a Go program on a simulated disk and network, not the real kernel and binary. See [properties](properties.md#linearizability).

## Why `wasip1`?

On `GOOS=wasip1` the Go runtime is single-threaded, has no async preemption, and reaches the outside world for time, randomness and blocking through three WASI calls: `clock_time_get`, `random_get` and `poll_oneoff`. seedfall implements those in the host, so the seed decides map iteration order, `select` choices, `math/rand` and crypto randomness, and every clock read. File and network calls are seedfall's too. That gives exact replay with the stock toolchain: no forked runtime, no `linkname` and no source rewriting outside the opt-in overlays, so new Go releases keep working. Each node runs as its own [wazero](https://wazero.io) instance, so a crash is a real process death. See [how it works](how-it-works.md).

## Does seedfall change my code?

Not by default. `net_overlay` adds `wasip1` files to the standard library's `net` package at build time, and `compat_overlay` swaps a dependency's `mmap` and `flock` files for shims in a copy under `.seedfall/compat`. Neither touches your source, `go.mod`, `go.sum` or module cache. Instead of `net_overlay`, you can call `sim.Listen` and `sim.Dial`, which fall back to `net.Listen` and `net.Dial` outside the simulator. See [networking](networking.md) and [compat](compat.md).

## What can't seedfall test?

These limits are also listed in the [README](../README.md#limitations):

- **Code that does not build for `wasip1`.** No cgo. Dependencies that need `mmap` or `flock` work through `compat_overlay`, but files named `_linux.go` and code using other unix calls still need a hand-written shim. Libraries that need cgo, such as RocksDB bindings or `mattn/go-sqlite3`, cannot run.
- **Time-based preemption.** Preemption is counted in function calls, not time: a spinning goroutine is preempted after about four million calls, and a loop that makes no function call only with `preempt.loops`. Even then the runtime switches goroutines only at a function entry, so a call-free loop keeps the node's other goroutines from running until it makes a call.
- **Exhaustive interleavings.** Interleavings inside a node come from the Go scheduler under a seeded random source. The preemption strategies sample interleavings rather than enumerate them, `pct`'s probability bound is a guide rather than a guarantee, and none of them detects data races.
- **Every file system.** `fs_model` covers ext4 `data=ordered`, an aggressive independent-pages model, and ext2 or btrfs directory reordering. XFS, `data=journal` and `nodelalloc` have no model of their own, a single directory operation is always atomic, and `Chmod` is not modelled.
- **Some network features.** UDP multicast is not simulated, and `WriteMsgUDP` with out-of-band data returns `ENOTSUP` ([networking](networking.md#udp)).
- **Memory shared between processes.** A mapping is coherent only with its own node's writes ([compat](compat.md#not-covered)).

Speed is a limit too: idle virtual time is free, but guest CPU and node boots are real time, and short seeds of large guests are dominated by boot cost ([how it works](how-it-works.md#speed)).

## Which platforms does it run on?

Linux, macOS and Windows. seedfall's tests run on all three in CI.

## How many seeds should I run?

Enough that a failure at the rate you care about would have shown up. `seedfall verify` computes this for 95% confidence, 299 seeds when no failure rate is known, and a sweep's utilization section says whether more seeds are likely to find anything new ([verify](verify.md), [reports](reports.md#utilization)). `seedfall explore` reaches states that need two rare events in one run ([explore](explore.md)).

## A failure looks wrong. Is it a seedfall bug?

Check its classification first: `nondeterministic` and `timeout` failures are explained in [troubleshooting](troubleshooting.md#failures-that-are-not-your-systems-bug). If seedfall produced an outcome a real machine could not, open a false positive issue with the replay command and schedule.
