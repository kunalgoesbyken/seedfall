# Troubleshooting

This page lists the errors and failure messages seedfall prints, with what causes each one and how to fix it. Find yours by its exact text. Parts in angle brackets, such as `<pkg>`, change from case to case. Commands print errors as `seedfall: <message>` and exit with code 1.

Do this first:

1. Run `seedfall doctor`. It checks the Go version, a `wasip1` build, `net_overlay`, the build cache, the working directory and free memory. For every check that is not `ok` it prints a fix.
2. In a new project, run `seedfall init`. It lists every package that will not build for `wasip1` (the WebAssembly target seedfall compiles your code for) and shows the chain of imports that pulls each one in.

## Setup and Go version

### Go is too old

```
  fail  go            <installed> is older than go<version>
        fix: install go<version> or newer from https://go.dev/dl/
```

`seedfall doctor` prints this when your Go is older than the version in seedfall's own `go.mod` (currently `go1.25.0`). Install that version or newer.

### No go command

```
  fail  go            no working go command on PATH: <error>
```

seedfall builds each node with your Go toolchain, so `go` must be on your `PATH`.

1. Install Go from https://go.dev/dl/.
2. Open a new terminal and run `go version` to check.
3. Run `seedfall doctor` again.

Until this passes, `seedfall doctor` skips the `wasip1 build`, `net_overlay` and `build cache` checks. If you use `net_overlay` or the generic compat shim, the same problem shows up as `go env GOROOT: <error>`.

### The net overlay does not support this Go version

```
net overlay: expected one "<anchor>" in <file>, found <n>; this Go version is not supported yet
```

With `"net_overlay": true`, seedfall patches a few functions in Go's own `net` package for `wasip1` (`net_fake.go`, `fd_fake.go` and `sockopt_fake.go`). Each patch looks for one exact line in `$GOROOT/src/net`. If a new Go release changed that line, seedfall refuses to patch instead of patching the wrong place. `seedfall doctor` shows this as a `warn` on the `net_overlay` check.

Fix it in one of two ways:

- Use a Go version that seedfall supports.
- Turn off `net_overlay` and call `sim.Listen` and `sim.Dial` in your code instead. They need no patch ([networking](networking.md)).

```
net overlay: <error>
```

seedfall could not read the files it patches from `$GOROOT/src/net`. Your Go install is missing its standard library source. Reinstall Go from https://go.dev/dl/.

## Building nodes for wasip1

### A node does not build

```
build <pkg>: exit status 1
```

For every `pkg` in the config, seedfall runs `GOOS=wasip1 GOARCH=wasm go build`. The compiler's own errors are printed above this line. The usual causes:

- **cgo.** cgo (Go code that calls C) is off under `wasip1`, so a package with cgo files does not build. `seedfall init` lists these under `cgo packages`, with a `cgo files: <files>` line and the chain `via <main> -> ... -> <pkg>`. Replace each with a pure-Go library, or stub it behind a build tag. Libraries such as RocksDB bindings or `mattn/go-sqlite3` cannot run under seedfall.
- **`mmap`, `flock` and other unix calls.** An error such as an undefined `syscall.Mmap`, `unix.Mmap` or `syscall.Flock` means the code uses operating system calls that WASI does not have. (`mmap` maps a file into memory. `flock` locks a file.) `seedfall init` lists each one under `wasip1 blockers` with a `shim:` line.
  - If the line names a shim, set `"compat_overlay": true` ([compat](compat.md)).
  - If it says `none: needs a shim, see docs/compat.md`, the dependency needs a hand-written shim, an upgrade, or a `go.mod` replace with an in-process version, as `examples/flockshim` does for `gofrs/flock`.
- **Platform-specific code in your own packages.** Move it behind build tags so that a `wasip1` version builds. `compat_overlay` never changes your own module.

### A compat shim does not match the library version

```
compat overlay: <pkg>/<file> declares <names>, which the wasip1 shim does not provide; this version is not supported yet
```

A shim replaces whole files, and only if it provides every top-level name those files declare. The version of the library you use declares something the shim does not, so seedfall stops instead of building a half replacement. Either pin the library to the version listed in [compat](compat.md), or add the missing declarations to the shim under `engine/compat` ([CONTRIBUTING](../CONTRIBUTING.md#adding-a-compat-shim)).

### golang.org/x/sys is not downloaded

```
compat overlay: golang.org/x/sys is not downloaded; run go mod download golang.org/x/sys
```

The generic shim adds `wasip1` versions of `Mmap` and `Flock` to a copy of `golang.org/x/sys/unix`, so that module must be in your module cache. Run the command from the message in the folder that holds your `go.mod`.

```
compat overlay: finding golang.org/x/sys: <error>
```

`go list -m golang.org/x/sys` failed in the config's folder. Run it yourself to see why. Usually your `go.mod` does not require `golang.org/x/sys`, or a module download fails.

### Not inside a module

```
compat overlay: <dir> is not inside a Go module
compat overlay: <pkg> is not part of a module
```

`compat_overlay` builds with a copy of your `go.mod` that adds a `replace` for each shimmed module. So the config's folder must be inside a Go module, and every shimmed package must come from one. Run seedfall from a folder with a `go.mod`, with module mode on (not `GO111MODULE=off`).

```
compat overlay: go mod edit: <error>: <output>
```

seedfall could not write the `replace` lines into its copy of `go.mod` under `.seedfall/compat`. The text after the colon comes from `go mod edit`. Check that `.seedfall` is writable and that your `go.mod` is valid.

### preempt.loops cannot instrument the binary

```
preempt.loops: function <n>: unsupported wasm opcode 0x<op>
preempt.loops: function <n>: unsupported wasm opcode 0xfc <sub>
```

With `"preempt": {"loops": true}`, seedfall reads every function of the compiled program and adds a counter after each `loop`. It hit an instruction it does not know, which a newer Go compiler may produce. Turn `preempt.loops` off and report your Go version in an issue.

## Config

These errors come from a check that runs before anything is built.

```
config: workload.name is required
config: every node needs a name
config: duplicate node name "<name>"
config: node "<name>" needs pkg or wasm
config: node "<name>" has an invalid pkg "<pkg>"
```

The workload and every node need a unique `name`. Every node also needs either a `pkg` (a Go package path, which seedfall builds for you) or a `wasm` file. [Configuration and faults](faults.md) lists every field.

```
config: node "<name>" sets both pkg and wasm, use one
<file>: json: unknown field "<name>"
```

A node names either a `pkg`, which seedfall builds into a `wasm` file, or a prebuilt `wasm`, never both. An "unknown field" is a key that is not part of the config. Look for a typo in the file, in a profile or in a `-set`.

```
config: faults.<name> must be between 0 and 1, got <value>
config: faults.<name> must not be negative, got <value>
config: <range> must be [min, max] with 0 <= min <= max, got [<min>, <max>]
config: duration must be positive, got <value>
config: watchdog must be positive, got <value>
config: faults.clock_jump_max must not be negative, got <value>
```

A number is out of range. A probability is outside 0 to 1, a `_per_sec` rate is negative, a range has a negative or reversed end, or a duration is not positive. [Config](config.md) says which rule covers which field.

```
config: no profile "<name>", have <profiles>
config: no profile "<name>", the config has no profiles
```

`-profile` named a profile that is not in the config's `profiles`. The first form lists the ones that exist ([config](config.md#profiles)).

```
config: -set "<expr>" is not path=value
config: -set <key>: <path> is not an object or a list
```

`-set` takes `path=value`. The path walks objects by key and lists by element name or index. The value is JSON or a bare string ([config](config.md#-set)).

```
config: <file> extends itself
<file>: extends must be the path of another config file
```

`extends` must name a different config file ([config](config.md#extends)).

```
config: preempt: unknown strategy "<name>", want one of <strategies>
config: preempt: interleave is shorthand for strategy random, but strategy is <name>
```

`preempt.strategy` is not one of the strategies listed, or `interleave` was combined with a different strategy ([how it works](how-it-works.md#strategies)).

```
bad seed range "<range>": seed 18446744073709551615 is the largest uint64, so the count of seeds or the next seed would wrap around, end the range at 18446744073709551614 or lower
```

A `-seeds` range cannot end at the largest 64-bit number. The sweep could not tell the end of the range from a wrap-around to seed 0.

```
the module cannot be instrumented for preemption or feedback: <reason>
```

`preempt.strategy`, `preempt.loops` and the feedback used by `explore` need a call counter that seedfall adds to Go `wasip1` programs. A prebuilt `wasm` from another toolchain, or one that does not import `sched_yield`, cannot take it. Build the node from Go, or turn those settings off.

```
-json and -report cannot be combined
timeout cannot be negative
```

`run` writes either one JSON object per seed or an HTML report, not both. `-timeout` must be zero (no limit) or positive.

```
report path <path> ends in .json, which is where the JSON report goes; use a .html path
```

`-report` names the HTML report. The JSON version is written next to it, with `.json` in place of the extension. An HTML report cannot go to a `.json` path, because it would be overwritten.

```
<file> line <n>: <json error>
```

A line of `.seedfall/history.jsonl` is not valid JSON, for example because an editor or a merge damaged it. `report`, `findings` and `report notify` refuse to read a history they cannot trust. Delete or repair the line named in the message ([history](reports.md#history-new-ongoing-resolved-regressed)).

```
listen <network>: the simulator supports only tcp, tcp4, tcp6
listen <network>: the simulator supports only udp, udp4, udp6
dial <network>: the simulator supports only tcp, tcp4, tcp6
```

`sim.Listen`, `sim.ListenPacket` or `sim.Dial` was given a network type the simulator does not have, such as `unix`. Use `tcp` or `udp` ([networking](networking.md#option-1-simlisten-and-simdial)).

## Failures that are not your system's bug

Every reported failure is classed as `bug`, `nondeterministic` or `timeout` ([reports](reports.md#classification)). The last two need a different fix from a bug.

### Watchdog

```
watchdog: <node>#<n> ran for more than 30s of real time without yielding (livelock or busy loop)
```

Classed as `timeout`. One node ran for longer than `watchdog` (default `30s`) of real time without ever waiting. Only one node runs at a time, and virtual time barely moves while it runs, so a loop that never waits freezes the whole simulation. The usual cause is a loop with no function call, which the default preemption cannot interrupt, or a very long calculation. The result depends on how fast and how busy your machine is, so the same seed may pass on a faster machine or with a lower `-j`.

To fix it:

1. Set `"preempt": {"loops": true}`. Loops with no function call are then interrupted and use up virtual time. A spin that never ends then fails as `liveness`, which does not depend on your machine ([how it works](how-it-works.md#loops), [`examples/busyloop`](../examples/busyloop)).
2. If the node really needs more real time, raise `watchdog`.
3. Or lower `-j`, so fewer nodes share the CPU.

### Nondeterminism

```
nondeterminism: two runs of seed <n> diverge at host call <n>: <call> vs <call>, after <call>; host calls <a>-<b> (...)
nondeterminism: two runs of seed <n> diverge at trace line <n>: "<line>" vs "<line>"
```

With `-check` (and in `verify`), every seed runs twice. The two runs made a different host call or wrote a different trace line. The first form names the first call that differs, with the node instance, call, target, argument and virtual time ([properties](properties.md#determinism-check)).

```
nondeterministic (replay diverges at trace line <n>: "<line>" vs "<line>")
nondeterministic (replay diverges at outcome: "<reason>" vs "<reason>")
```

seedfall replays a failure to classify it, and the replay did not match the original run.

seedfall controls everything a program normally reads: time, randomness, scheduling, disk and network. All of it replays exactly. So a difference means something from outside got in. Check these, in order:

1. Did your program's binary change between the runs? A rebuilt package, another Go toolchain or a different source tree makes a different binary and can change the run.
2. Did input from the host change? Examples are seeded disk images or fixture files that the config reads.
3. Did the watchdog or a `-timeout` limit fire in one run and not the other? Both depend on real time.

If none of these applies, it is a bug in seedfall. Open a bug report with the `-check` output, the config and the seed.

### Deadlock

```
deadlock: every node is blocked with no timer or pending message
deadlock: every node has exited
```

Classed as `bug`. In the first message every node is waiting and nothing can wake one. In the second, no node is left to run before the workload finished.

The first is often a real bug. For example, a retry loop waits for a reply after a partition dropped the connection, and it never set a deadline. It is also what you see when nodes cannot reach each other at all. Without `net_overlay` or `sim.Listen`, Go's `net` package on `wasip1` is a fake that lives inside one process, so a listener on one node cannot be reached from another ([networking](networking.md#without-either)).

The second usually means the nodes exited and nothing restarts them.

### Liveness

```
liveness: <node> did not finish within <within> of the recovery phase starting at <time>
```

With `liveness` on, faults stop at the recovery phase, the network heals and crashed nodes restart. The workload or the `finally` check then has `liveness.within` to finish. A system that never recovers has a real bug. If it recovers but slowly, raise `within` ([properties](properties.md#liveness-recovery-and-finally-checks)).

### A node exits or panics

```
<node>#<n> exited
<node>#<n> crashed: <error>
```

The first line means a node, other than the workload or the `finally` check, exited with status 0. A server that returns from `main` counts as a failure. The second means a node exited with an error or panicked. With `"supervise": true`, seedfall restarts a node that exits with an error after `restart_after`, as systemd would. A Go panic (exit code 2) always fails the run ([faults](faults.md#world)).
