# Troubleshooting

This page lists the errors and failure messages seedfall prints, keyed by their exact text, with what causes each one and how to fix it. Parts in angle brackets, such as `<pkg>`, vary. Commands print errors as `seedfall: <message>` and exit 1.

Start with `seedfall doctor`. It checks the Go version, a `wasip1` build, `net_overlay`, the build cache, the working directory and free memory, and prints a fix for each check that is not `ok`. In a new module, `seedfall init` reports every package that will not build for `wasip1`, with the import chain to it.

## Setup and Go version

### Go is too old

```
  fail  go            <installed> is older than go<version>
        fix: install go<version> or newer from https://go.dev/dl/
```

`seedfall doctor` prints this when the installed Go is older than the version in seedfall's own `go.mod` (currently `go1.25.0`). Install that version or newer.

### No go command

```
  fail  go            no working go command on PATH: <error>
```

seedfall builds every node with your Go toolchain, so it needs `go` on `PATH`. Install Go from https://go.dev/dl/. `seedfall doctor` skips the `wasip1 build`, `net_overlay` and `build cache` checks until this passes. When `net_overlay` or the generic compat shim is built, the same problem shows up as `go env GOROOT: <error>`.

### The net overlay does not support this Go version

```
net overlay: expected one "<anchor>" in <file>, found <n>; this Go version is not supported yet
```

With `"net_overlay": true`, seedfall patches a few functions of the installed Go's `net` package for `wasip1` (`net_fake.go`, `fd_fake.go` and `sockopt_fake.go`). Each patch looks for one exact line in `$GOROOT/src/net`. If a Go release changes that line, the patch is refused rather than applied in the wrong place. `seedfall doctor` reports it as a `warn` on the `net_overlay` check.

Fix: use a Go version seedfall supports, or drop `net_overlay` and make nodes reachable with `sim.Listen` and `sim.Dial`, which need no patch ([networking](networking.md)).

```
net overlay: <error>
```

The patched files could not be read from `$GOROOT/src/net`. The Go installation is missing its standard library source; reinstall Go from https://go.dev/dl/.

## Building nodes for wasip1

### A node does not build

```
build <pkg>: exit status 1
```

seedfall runs `GOOS=wasip1 GOARCH=wasm go build` for each `pkg` in the config, and the compiler's own errors are printed above this line. The usual causes:

- **cgo.** cgo is off under `wasip1`, so a package with cgo files does not build. `seedfall init` lists these under `cgo packages` with a `cgo files: <files>` line and the chain `via <main> -> ... -> <pkg>`. Replace each with a pure-Go alternative, or stub it behind a build tag. Libraries such as RocksDB bindings or `mattn/go-sqlite3` cannot run under seedfall.
- **`mmap`, `flock` and other unix calls.** Errors such as an undefined `syscall.Mmap`, `unix.Mmap` or `syscall.Flock` come from code that WASI cannot run. `seedfall init` lists each under `wasip1 blockers` with a `shim:` line. If it names a shim, set `"compat_overlay": true` ([compat](compat.md)). If it says `none: needs a shim, see docs/compat.md`, the dependency needs a hand-written shim, an upgrade, or a `go.mod` replace with an in-process version, as `examples/flockshim` does for `gofrs/flock`.
- **Platform-specific code in your own packages.** Move it behind build tags so that a `wasip1` version builds. `compat_overlay` never changes your own module.

### A compat shim does not match the library version

```
compat overlay: <pkg>/<file> declares <names>, which the wasip1 shim does not provide; this version is not supported yet
```

A shim replaces whole files, and only if it provides every top-level declaration in them. The library version you depend on declares something the shim does not, so seedfall stops instead of building a partial replacement. Pin the library to the version listed in [compat](compat.md), or add the missing declarations to the shim under `engine/compat` ([CONTRIBUTING](../CONTRIBUTING.md#adding-a-compat-shim)).

### golang.org/x/sys is not downloaded

```
compat overlay: golang.org/x/sys is not downloaded; run go mod download golang.org/x/sys
```

The generic shim adds `wasip1` versions of `Mmap` and `Flock` to a copy of `golang.org/x/sys/unix`, so the module has to be in the module cache. Run the command in the message from the directory of your `go.mod`.

```
compat overlay: finding golang.org/x/sys: <error>
```

`go list -m golang.org/x/sys` failed in the config's directory. Run it yourself to see why, usually a `go.mod` that does not require `golang.org/x/sys` or a module download that fails.

### Not inside a module

```
compat overlay: <dir> is not inside a Go module
compat overlay: <pkg> is not part of a module
```

`compat_overlay` builds with a copy of your `go.mod` that adds a `replace` for each shimmed module, so the config's directory must be inside a module and every shimmed package must come from one. Run seedfall from a module with a `go.mod` and module mode on (not `GO111MODULE=off`).

```
compat overlay: go mod edit: <error>: <output>
```

seedfall could not write the `replace` lines into its copy of `go.mod` under `.seedfall/compat`. The output after the colon is from `go mod edit`. Check that `.seedfall` is writable and that `go.mod` parses.

### preempt.loops cannot instrument the binary

```
preempt.loops: function <n>: unsupported wasm opcode 0x<op>
preempt.loops: function <n>: unsupported wasm opcode 0xfc <sub>
```

With `"preempt": {"loops": true}`, seedfall decodes every function of the compiled module to add a counter after each `loop`. It stopped at an instruction its decoder does not know, which a newer Go compiler could emit. Turn `preempt.loops` off and report the Go version in an issue.

## Config

```
config: workload.name is required
config: every node needs a name
config: duplicate node name "<name>"
config: node "<name>" needs pkg or wasm
config: node "<name>" has an invalid pkg "<pkg>"
```

The config is checked before anything is built. Every node and the workload need a unique `name`, and every node needs a `pkg` (a Go package path, built for you) or a `wasm` file. [Configuration and faults](faults.md) lists every field.

```
config: no profile "<name>", have <profiles>
config: no profile "<name>", the config has no profiles
```

`-profile` named a profile that is not in the config's `profiles` ([config](config.md#profiles)).

```
config: -set "<expr>" is not path=value
config: -set <key>: <path> is not an object or a list
```

`-set` takes `path=value`, where the path walks objects by key and lists by element name or index, and the value is JSON or a bare string ([config](config.md#-set)).

```
config: <file> extends itself
<file>: extends must be the path of another config file
```

`extends` must name a different config file ([config](config.md#extends)).

```
config: preempt: unknown strategy "<name>", want one of <strategies>
config: preempt: interleave is shorthand for strategy random, but strategy is <name>
```

`preempt.strategy` is not one of the listed strategies, or `interleave` was combined with a different strategy ([how it works](how-it-works.md#strategies)).

```
-json and -report cannot be combined
timeout cannot be negative
```

`run` writes either one JSON object per seed or an HTML report, not both, and `-timeout` must be zero (no budget) or positive.

```
report path <path> ends in .json, which is where the JSON report goes; use a .html path
```

`-report` names the HTML report and the JSON version is written next to it with `.json` in place of the extension, so an HTML report cannot go to a `.json` path without being overwritten.

```
<file> line <n>: <json error>
```

A line of `.seedfall/history.jsonl` is not valid JSON, for example after an editor or a merge damaged it. `report`, `findings` and `report notify` refuse to read a history they cannot trust. Delete or repair the named line ([history](reports.md#history-new-ongoing-resolved-regressed)).

```
listen <network>: the simulator supports only tcp, tcp4, tcp6
listen <network>: the simulator supports only udp, udp4, udp6
dial <network>: the simulator supports only tcp, tcp4, tcp6
```

`sim.Listen`, `sim.ListenPacket` or `sim.Dial` got a network the simulator does not have, such as `unix`. Use `tcp` or `udp` ([networking](networking.md#option-1-simlisten-and-simdial)).

## Failures that are not your system's bug

Every reported failure is classed as `bug`, `nondeterministic` or `timeout` ([reports](reports.md#classification)). The last two need a different fix from a bug.

### Watchdog

```
watchdog: <node>#<n> ran for more than 30s of real time without yielding (livelock or busy loop)
```

Classified `timeout`. A node ran without blocking for longer than `watchdog` (default `30s`) of real time. Only one node runs at a time and virtual time barely moves while it runs, so a loop that never blocks holds the whole simulation. The usual cause is a loop with no function call, which the default preemption cannot interrupt, or a long computation. Because it depends on how fast the host is and how loaded, the same seed may pass on a faster machine or with a lower `-j`.

Fix: set `"preempt": {"loops": true}` so call-free loops are preempted and spend virtual time; a spin that never ends then fails as `liveness`, which does not depend on the host ([how it works](how-it-works.md#loops), [`examples/busyloop`](../examples/busyloop)). If the node really needs more real time, raise `watchdog`, or lower `-j` so fewer guests share the CPU.

### Nondeterminism

```
nondeterminism: two runs of seed <n> diverge at host call <n>: <call> vs <call>, after <call>; host calls <a>-<b> (...)
nondeterminism: two runs of seed <n> diverge at trace line <n>: "<line>" vs "<line>"
```

With `-check` (and in `verify`), every seed runs twice, and the two runs made a different host call or wrote a different trace line. The first form names the first call that differs, with the node instance, call, target, argument and virtual time ([properties](properties.md#determinism-check)).

```
nondeterministic (replay diverges at trace line <n>: "<line>" vs "<line>")
nondeterministic (replay diverges at outcome: "<reason>" vs "<reason>")
```

A failure is replayed to classify it, and the replay did not match the original run.

Everything a guest normally reads, time, randomness, scheduling, disk and network, is owned by seedfall and replays exactly, so a divergence means something outside that leaked in. Check, in this order:

- whether the guest binary changed between the runs: a rebuilt package, another Go toolchain or a different source tree produces a different binary and can change the run;
- whether input from the host changed, such as seeded disk images or fixture files that the config reads;
- whether the watchdog or a `-timeout` budget fired in one run and not the other, since both depend on real time.

If none applies, it is a bug in seedfall. Open a bug report with the `-check` output, the config and the seed.

### Deadlock

```
deadlock: every node is blocked with no timer or pending message
deadlock: every node has exited
```

Classified `bug`. Every node is waiting and nothing can wake one, or no node is left to run before the workload finished. The first is often a real bug, for example a retry loop that waits for a reply after a partition dropped the connection without setting a deadline. It is also what happens when nodes cannot reach each other at all: without `net_overlay` or `sim.Listen`, Go's `net` package on `wasip1` is an in-process fake, and a listener on one node is unreachable from another ([networking](networking.md#without-either)). The second usually means the nodes exited and nothing restarts them.

### Liveness

```
liveness: <node> did not finish within <within> of the recovery phase starting at <time>
```

With `liveness`, faults stop at the recovery phase, the network heals and crashed nodes restart; the workload or `finally` check then has `liveness.within` to finish. A system that never recovers is a real bug. If it recovers but slowly, raise `within` ([properties](properties.md#liveness-recovery-and-finally-checks)).

### A node exits or panics

```
<node>#<n> exited
<node>#<n> crashed: <error>
```

The first line is a node, other than the workload or `finally` check, that exited with status 0; a server that returns from `main` is treated as a failure. The second is a node that exited with an error or panicked. `"supervise": true` restarts a node that exits with an error after `restart_after`, as systemd would, but Go panics (exit code 2) always fail the run ([faults](faults.md#world)).
