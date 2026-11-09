# Roadmap

This lists what seedfall does not do yet and where work is likely to go next. Nothing here is done unless it says so, there are no dates, and priorities follow what users run into. The current limits are in the [README](README.md#limitations) and the [FAQ](docs/faq.md#what-cant-seedfall-test). To work on one of these, open an issue first ([CONTRIBUTING](CONTRIBUTING.md)).

## Platform reach

- **More compat shims.** `compat_overlay` covers bbolt, pebble v2 and badger v4 with dedicated shims, and other callers of `Mmap`, `Msync` and `Flock` through the generic shim. Not covered yet: files named `_linux.go`, other unix calls, `mremap`, and libraries with no verified shim, such as `boltdb/bolt`, `hashicorp/raft-boltdb` before `v2`, `dgraph-io/ristretto` before `v2` and `cockroachdb/pebble` before `v2` ([compat](docs/compat.md#not-covered)). Each new shim needs an example that shows acknowledged writes surviving crashes, as the existing ones do.
- **UDP gaps.** UDP is simulated with seeded latency, reordering, loss and duplication. Multicast is not simulated, and `WriteMsgUDP` with out-of-band data and raw reads with flags return `ENOTSUP` ([networking](docs/networking.md#udp)).
- **cgo.** Code must build for `wasip1`, so cgo is out of reach. There is no plan to change this; the goal is to make the `seedfall init` report and the shims cover as much pure-Go code as possible.

## Scheduling

- **Time-based preemption.** Preemption is counted in function calls (and loop iterations with `preempt.loops`), not time, and the Go runtime on `wasip1` switches goroutines only at a function entry, so a call-free loop still keeps its node's other goroutines from running until it makes a call. Getting closer to async preemption on `wasip1` is open.
- **Interleaving coverage.** The `random`, `pct` and `calls` strategies sample interleavings rather than enumerate them, and `pct` chooses change points but not goroutine priorities, so its bound is a guide. A fuller PCT and some measure of which interleavings a sweep has covered are open.

## Finding bugs faster

- **Coverage guidance.** `seedfall explore` branches on new property outcomes, fault shapes, buggify firings and functions reached. Function coverage counts function entries, not basic blocks or branches ([explore](docs/explore.md#how-it-works)). Finer coverage would give exploration more signal to follow.
- **Snapshots.** Nothing is snapshotted today: `seedfall cause`, `explore` branches and `debug` re-run a seed from the start to reach a moment, which exact replay makes correct but which costs a full re-run each time ([explore](docs/explore.md#throughput)). Snapshotting guest state would make branching from late in a long run cheaper.
- **Boot cost.** Short seeds of large guests are dominated by booting each node (0.1-0.3s per start for nats-server) ([how it works](docs/how-it-works.md#speed)).

## Fidelity

- **More file system models.** `fs_model` covers ext4 `data=ordered`, an aggressive independent-pages model, and ext2 or btrfs directory reordering. XFS, `data=journal` and `nodelalloc` have no model, a single directory operation is always atomic, and `Chmod` is not modelled because WASI has no such call ([faults](docs/faults.md#file-system-models)).
- **Mapped pages.** A written-back mapped page has its content at the moment of the crash; an older version from an earlier writeback is not generated, and two mappings of one file see each other's writes only after a flush ([compat](docs/compat.md)).

## Project

- **A release from the automated pipeline.** v0.2.0 predates the GoReleaser pipeline, so Homebrew, Scoop, the install script and the Docker image wait for the next release ([install](docs/install.md)).
- **Error links.** The CLI could point each error in [troubleshooting](docs/troubleshooting.md) at its entry.
