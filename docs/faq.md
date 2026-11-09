# FAQ

Short answers to the questions people ask most, with links to the pages that go into detail. Start with the first section if you are comparing seedfall to something you already know.

Two words come up a lot. A **seed** is a number that decides everything random in one run, so the same seed always gives the same run. A **fault** is something that goes wrong on purpose, such as a crash or a lost message.

## How is seedfall different from Antithesis?

Antithesis runs any kind of program, in any language, inside a special virtual machine that can replay everything exactly. seedfall only runs Go programs, inside one process, with a simulated disk and network. In exchange, seedfall runs on your laptop and in CI with no service and no account, and it is open source.

If your program already uses the Antithesis Go SDK (a library for writing checks in your code), it runs under seedfall unchanged. The SDK's `assert.Always`, `Sometimes`, `Reachable`, the numeric and grouped variants, `lifecycle.SetupComplete` and `lifecycle.SendEvent` become seedfall properties (checks), setup and trace events. `seedfall cause` uses the same idea as Antithesis's multiverse debugging: it looks at how likely the failure was from each moment of the run. Instead of VM snapshots it replays exactly from the start. See [Antithesis compatibility](antithesis.md).

## How is it different from gosim?

[gosim](https://github.com/jellevandenhooff/gosim) is also deterministic simulation testing for Go, with simulated machines, a network, a file system and crashes. Deterministic means the same input always gives the same run. gosim rewrites your program and its dependencies so they run on its own simulated runtime, at the level of system calls.

seedfall does not rewrite code. It compiles your program with the normal Go toolchain for `GOOS=wasip1` (a WebAssembly target, explained [below](#why-wasip1)) and controls everything random at that boundary: time, randomness and waiting, plus its own file system and network calls. Each node is a separate WebAssembly instance, so a crash kills the whole process and a restart boots a fresh one over the disk that survived. Check gosim's own documentation for its current features instead of relying on this page.

## Why not `testing/synctest`?

`testing/synctest` is part of Go's standard library. It runs a test's goroutines (Go's lightweight threads) in a "bubble" with a fake clock. The clock jumps ahead whenever every goroutine in the bubble is waiting. This makes time-based unit tests fast and repeatable inside one `go test` process.

It does not simulate a disk or a network, crash or restart processes, inject faults, or record a run you can replay and shrink. The order in which goroutines interleave still comes from the real scheduler.

seedfall is for whole services: several nodes as separate processes on one virtual clock, with crashes, torn writes (a write that a crash leaves half saved), fsync errors (fsync is the call that forces data onto the disk), partitions (nodes that cannot reach each other) and clock faults. Any failure replays from its seed. The two fit together: use `synctest` for the timing logic of one component, and seedfall for what your service does when the machine fails under it.

## Does seedfall replace `go test -race`?

No. On `wasip1` the Go runtime runs one thing at a time, so goroutines never really run in parallel. The overlapping memory accesses that the race detector looks for never happen there. seedfall's `random`, `pct` and `calls` preemption strategies do find ordering bugs, such as a check-then-act race between goroutines, but they do not detect data races the way `-race` does. Keep running `-race` on your unit tests. seedfall's own CI does.

## How does it compare with Jepsen?

Jepsen tests real deployments of a database, on real or virtual machines, in any language. It injects faults into the real operating system and network, then checks the recorded history for consistency problems. It finds bugs in the system as shipped, but a failing run cannot be replayed exactly.

seedfall borrows from Jepsen. Its network partition shapes follow Jepsen's "nemeses". It records histories in Jepsen's invoke and completion shape. It checks `sim.Txn` histories for the same Elle anomalies (`G0`, `G1a`, `G1b`, `G1c`, `G-single`, `G2`, which are named kinds of transaction inconsistency).

The difference is that every seedfall run is deterministic. A failure comes with a seed, an exact fault schedule and a trace, and `seedfall shrink` cuts the schedule down to the faults that matter. The cost is that seedfall tests a Go program on a simulated disk and network, not the real kernel and the real binary. The checker for **linearizability** (the rule that concurrent operations must look as if they happened one at a time, in an order that fits real time) is described in [properties](properties.md#linearizability).

## Why `wasip1`?

`wasip1` is a Go build target: `GOOS=wasip1` compiles your program to WebAssembly that talks to the outside world through WASI, a small, standard system interface. For seedfall this has a useful property. The Go runtime on `wasip1` is single-threaded and has no asynchronous preemption, and a program reaches the outside world for time, randomness and waiting through only three WASI calls: `clock_time_get`, `random_get` and `poll_oneoff`.

seedfall implements those three calls itself. So the seed decides map iteration order, `select` choices, `math/rand`, crypto randomness and every clock read. File and network calls are seedfall's too. The result is exact replay with the standard toolchain: no forked Go runtime, no `linkname` tricks and no source rewriting outside the opt-in overlays, so new Go releases keep working. Each node runs as its own [wazero](https://wazero.io) instance, so a crash is a real process death. See [how it works](how-it-works.md).

## Does seedfall change my code?

Not by default. Two opt-in settings change how things are built, never your files:

- `net_overlay` adds `wasip1` files to the standard library's `net` package at build time, so unmodified code can use the simulated network.
- `compat_overlay` swaps a dependency's `mmap` and `flock` files for stand-ins, in a copy under `.seedfall/compat`.

Neither touches your source, `go.mod`, `go.sum` or module cache. Instead of `net_overlay` you can call `sim.Listen` and `sim.Dial`, which fall back to `net.Listen` and `net.Dial` outside the simulator. See [networking](networking.md) and [compat](compat.md).

## What can't seedfall test?

The README has a short version of this list under [When it's not a fit](../README.md#when-its-not-a-fit). The full list:

- **Code that does not build for `wasip1`.** There is no cgo (Go code that calls C). Dependencies that need `mmap` or `flock` work through `compat_overlay`, but files named `_linux.go` and code that uses other unix calls still need a hand-written stand-in. Libraries that need cgo, such as RocksDB bindings or `mattn/go-sqlite3`, cannot run.
- **Time-based preemption.** seedfall counts function calls instead of time. A goroutine that spins is interrupted after about four million calls. A loop that makes no function call is interrupted only with `preempt.loops`. Even then the runtime switches goroutines only when a function is entered, so a call-free loop keeps the node's other goroutines from running until it makes a call.
- **Every possible ordering of goroutines.** Inside a node, the ordering comes from the Go scheduler under a seeded random source. The preemption strategies sample orderings rather than list all of them. `pct`'s probability bound is a guide, not a guarantee. None of them detects data races.
- **Every file system.** `fs_model` covers ext4 `data=ordered`, an aggressive independent-pages model, and ext2 or btrfs directory reordering. XFS, `data=journal` and `nodelalloc` have no model of their own. A single directory operation is always atomic, and `Chmod` is not modelled.
- **Some network features.** UDP multicast is not simulated, and `WriteMsgUDP` with out-of-band data returns `ENOTSUP` ([networking](networking.md#udp)).
- **Memory shared between processes.** A memory mapping is consistent only with its own node's writes ([compat](compat.md#not-covered)).

Speed is a limit too. Idle virtual time is free, but the CPU time your code uses and the time to start each node are real. Short runs of large programs are dominated by start-up cost ([how it works](how-it-works.md#speed)).

## Which platforms does it run on?

Linux, macOS and Windows. seedfall's tests run on all three in CI.

## How many seeds should I run?

Enough that a failure at the rate you care about would have shown up. `seedfall verify` works this out for 95% confidence. With no known failure rate it asks for 299 seeds. A sweep's utilization section tells you whether more seeds are likely to find anything new ([verify](verify.md), [reports](reports.md#utilization)). For bugs that need two rare events in one run, `seedfall explore` is better than more plain seeds ([explore](explore.md)).

## A failure looks wrong. Is it a seedfall bug?

First check the failure's classification. Failures marked `nondeterministic` or `timeout` are explained in [troubleshooting](troubleshooting.md#failures-that-are-not-your-systems-bug). If seedfall produced an outcome that a real machine could not, open a false positive issue and include the replay command and the fault schedule.
