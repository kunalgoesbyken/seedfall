---
name: seedfall-setup
description: Set up seedfall deterministic simulation testing in a Go module that has no seedfall.json yet. Checks that the services build for wasip1, scaffolds seedfall.json and a workload with seedfall init, wires the workload to the service, and runs the first seed sweep. Use when asked to add seedfall, crash testing, deterministic simulation testing or fault injection to a Go service.
compatibility: Requires Go 1.25 or newer and the seedfall CLI. The seedfall MCP server is optional; every step has a CLI form.
---

# Set up seedfall in a Go module

seedfall builds each node (a Go `main` package) for `GOOS=wasip1`, runs every node on one simulated clock, and crashes, tears and partitions them from a seed. The workload is another `main` package: it drives the nodes and checks invariants with `sim.Assert` and friends. Setup is done when a 20-seed sweep runs and its property table shows that the workload got acknowledged writes and that a fault interrupted it.

## 1. Install

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
```

## 2. Inspect and scaffold

From the module root:

```sh
seedfall init -n      # report only, write nothing
seedfall init         # write seedfall.json and seedfall/workload/main.go
```

Add `-json` for the structured report. Over MCP, call `seedfall_init` with `{"dir": ".", "dry_run": true}`, then without `dry_run`. Init never overwrites: existing files are listed as kept.

Read the report in this order:

1. **wasip1 blockers.** Packages that fail to compile for wasip1, each with the compiler error and the import chain from the node. Nothing else works until these are gone.
   - `own_module: true`: move the platform-specific file behind a build tag and give wasip1 a portable version.
   - A dependency: upgrade it first, since newer versions often build for wasip1. Otherwise add a `replace` in go.mod that points at a small in-process shim. `examples/flockshim` in the seedfall repository replaces `github.com/gofrs/flock` this way.
2. **cgo packages.** cgo is off under wasip1, so these either fail to build or fall back to a stub. Use a pure-Go alternative or stub them behind a build tag.
3. **main packages.** Every `main` package that imports `net` becomes a node. If init picked the wrong ones, such as a CLI, an admin tool or an existing test client, delete `seedfall.json` and rerun with one `-node` per real server, for example `seedfall init -node ./cmd/server`.
4. **next steps.** Follow them in order.

Some dependencies compile but fail at runtime under wasip1. For example, gofrs/flock returns `ErrUnsupported`. These show up as node errors in the first run's trace; shim them the same way.

If the module does not require seedfall yet, run `go get github.com/kunalgoesbyken/seedfall@latest`. The workload imports `github.com/kunalgoesbyken/seedfall/sim`.

## 3. Wire the workload

Open `seedfall/workload/main.go` and implement `put` and `get` against the service's real API:

- **Address.** A node is reachable by its name from `seedfall.json`. Set the workload's `-addr` argument in `seedfall.json` to `<node>:<port the service listens on>`. Init guesses port 8080.
- **Network.** If the nodes import `github.com/kunalgoesbyken/seedfall/sim`, dial with `sim.Dial`. Otherwise init turned on `net_overlay`, and plain `net.Dial` and `net/http` reach the simulated nodes. UDP works the same way, through `sim.ListenPacket` or plain `net.ListenUDP` under `net_overlay`; add `udp_loss` and `udp_dup` to the faults for a program that uses it.
- **Acknowledgement.** `put` returns nil only when the service acknowledged the write. Return an error for everything else. Writes whose outcome is unknown are simply not tracked.
- **Timeouts.** Set a deadline on every request (`conn.SetDeadline`, or `http.Client{Timeout: ...}`), so a crash or partition does not block the workload forever.

Keep the two `sim.Sometimes` lines. They prove the workload exercised the service and that faults hit it.

## 4. First sweep

```sh
seedfall run -c seedfall.json -seeds 1-20
```

Over MCP: `seedfall_run` with `{"config": "seedfall.json", "seeds": "1-20"}`.

| Output | Meaning | Next |
|---|---|---|
| `build ...` error | A wasip1 compile error | Go back to step 2 |
| `the workload got acknowledged writes` never true | `put` never succeeded | Check `-addr` and the port. `seedfall replay -c seedfall.json -seed 1` prints every node's output in the trace |
| `a fault interrupted the workload` never true | Faults never reached the workload | Raise `crash_per_sec` or lengthen `duration` |
| `FAIL seed N` with `class: bug` | Possibly a real bug | Use the `seedfall-triage` skill |
| `ok  20 seeds passed` | Setup is done | Use the `seedfall-properties` skill |

Then confirm that nothing leaks host nondeterminism:

```sh
seedfall run -c seedfall.json -seeds 1-20 -check
```

Once 20 seeds pass, harden the faults without editing the base config. Init wrote three profiles into `seedfall.json`; run them in order and treat each failure as a finding for the `seedfall-triage` skill:

```sh
seedfall run -c seedfall.json -profile strict -seeds 1-20       # strict POSIX directory durability
seedfall run -c seedfall.json -profile resilient -seeds 1-20    # adds the resilient fault preset
seedfall run -c seedfall.json -profile everything -seeds 1-20   # every fault, including disk corruption
```

Over MCP, pass `"profile": "resilient"` to any tool. Keep new variants as profiles in the same file, and use `"set": ["faults.crash_per_sec=2"]` or `-set` for a one-off change. If the service must start from existing data, such as a fixture database or a config file, give the node `"disk": "dir"` or `"files": {"guest/path": "host/path"}` instead of baking them into the binary.

The config fields and their defaults are in the MCP resource `seedfall://schema/config`. What each one means is in `seedfall://docs/faults` (docs/faults.md), and profiles, presets, `-set`, seeded disks and upgrades are in `seedfall://docs/config` (docs/config.md).
