# Contributing to seedfall

Bug reports, new systems to test, fixes and new compat shims are all welcome. This page covers how to build and test seedfall, what each kind of change needs, and how commits are written.

Before you start on something large, open an issue so the approach can be agreed first. Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Setup

You need the Go version in [`go.mod`](go.mod) or newer. Nothing else: seedfall builds guests with the stock toolchain for `GOOS=wasip1` and runs them in [wazero](https://wazero.io), so no other runtime is involved.

```sh
git clone https://github.com/kunalgoesbyken/seedfall
cd seedfall
go build ./cmd/seedfall
./seedfall doctor
```

`seedfall doctor` checks the Go version, that a `wasip1` build works, that `net_overlay` patches the installed Go's `net` package, that the build cache and working directory are writable, and how much memory is free. Fix anything it marks `fail` before running the tests.

## Tests

```sh
go test -short ./...    # skips the tests that build and run a wasip1 guest; quick
go test ./...           # everything, including the end-to-end guest in engine/testdata/guest
go test -race ./...     # what CI runs on Linux, macOS and Windows; needs cgo
go test ./report ./verify -update   # rewrite golden files after an intended output change, then review the diff
```

CI also runs `gofmt -l .`, `go vet -stdmethods=false ./...`, `GOOS=wasip1 GOARCH=wasm go vet ./sim`, staticcheck and govulncheck. Run those before you push:

```sh
gofmt -l .
go vet -stdmethods=false ./...
GOOS=wasip1 GOARCH=wasm go vet ./sim
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

[docs/testing.md](docs/testing.md) lists what each test file proves. A change to the disk, network, scheduler, fault or shrink model needs a test there that pins the new behavior down.

### The example regression suite

```sh
bash scripts/regress.sh
```

This builds `seedfall` into the repository root and runs every example against its expected result: each config must pass or fail as recorded, saved fault schedules must replay to the recorded outcome, shrinks must reach the recorded size, and selected replays must be byte-identical. It also drives the GitHub Action script (`scripts/action-run.sh`) and the `version`, `help` and `doctor` commands. It prints one `PASS` or `FAIL` line per check and exits with the number of failed checks.

It is expensive. It takes hours, uses every core for the sweeps, and needs enough memory for many guests at once, since several examples run large guests such as nats-server, pebble and badger. CI runs it nightly (`regress.yml`) rather than on every pull request, so you usually do not need to run all of it. If your change touches an example or its config, run the `expect` lines for that example by hand instead, for example:

```sh
cd examples/diskv
seedfall run -c seedfall.strict.json -seeds 1-300
```

## Adding an example

An example is a directory under [`examples/`](examples) with:

- its own `go.mod`, with `replace github.com/kunalgoesbyken/seedfall => ../..`, pinned to the library version the example was written against (Dependabot leaves the examples alone on purpose);
- the node and workload packages, usually `server/` and `client/`;
- one `seedfall.json`, plus one config or profile per variant you want to show (for example a fixed version, or `strict_dirs`);
- a `README.md` that says what the example shows, which configs pass and which fail, and the replay command for each failure.

Then add `expect` lines to `scripts/regress.sh` for every config with the seed range and the expected `pass` or `fail`, and an `expect_replay` or `deterministic` line if the example ships a fault schedule. Add a row to the Examples table in [README.md](README.md). Every failing config needs a passing counterpart, or a negative control, so the example shows that the check is real and not vacuous.

## Adding a compat shim

`compat_overlay` swaps a dependency's `mmap` and `flock` code for `wasip1` versions ([docs/compat.md](docs/compat.md)). A shim is a directory of `.go.txt` files under [`engine/compat`](engine/compat), named by the import path it replaces, for example `engine/compat/go.etcd.io/bbolt/bolt_wasip1.go.txt`.

At build time seedfall copies the dependency's module into `.seedfall/compat`, adds the shim files, and removes each file that declares something the shim declares. Every top-level declaration in a removed file must then exist in the shim, or the build stops with an error that names the missing ones. Nothing is patched by text.

Before adding a library-specific shim, check whether the generic shim already covers it: `seedfall init` names the shim it would use for each dependency. The generic shim handles callers of `Mmap`, `Msync` and `Flock` in unix-constrained files; files named `_linux.go` and other unix calls need a dedicated shim.

A new shim needs:

- an example under `examples/` that runs the library unmodified through `compat_overlay` and passes a sweep with crashes and torn writes, plus a negative control that fails (see `examples/bboltkv`, `examples/pebblekv` and `examples/badgerkv`);
- the library and version added to the list in [docs/compat.md](docs/compat.md), with anything the shim does not cover.

A shim that makes a library build is not enough: `pebble` before `v2` built with a shim but lost acknowledged writes on restart, so it is not shipped. Verify that acknowledged writes survive before submitting.

## Writing up a finding

Findings in other projects live in [`docs/findings`](docs/findings), one file per bug, and each is listed in [docs/findings/README.md](docs/findings/README.md). Follow the existing reports:

- The title states the bug and its consequence in one sentence.
- **Upstream:** a link to the issue or pull request filed in the project, or `not filed yet.`
- **Repo:** the project and the exact version or commit tested.
- **Severity:** what it takes to hit the bug (a crash, a fault, a strict POSIX filesystem) and what is lost.
- `## Summary`: the root cause with file and line references in the upstream code.
- `## Reproduction`: the workload and what it checks, the `seedfall replay` command from the example's directory, the shrunk schedule from `seedfall shrink`, and the failure message. A table of configs and results helps, including the negative controls.
- `## Suggested fix`: the smallest change that closes the hole.
- `## Fix verified`: the patched copy and how many seeds it passed.

Report the bug upstream before the write-up is published here, and link it in **Upstream:**. Give maintainers the reproduction first; if the bug is a security issue in their project, use that project's private reporting process and wait for their fix. If a finding cannot be filed (the repository is archived or private), say so in the report.

`site/proof/` is generated from these files by `scripts/site-proofs.sh`, and `site/llms.txt` and `site/llms-full.txt` from `docs/` by `scripts/site-llms.sh`. Rerun both after changing a finding or a doc, and commit the output.

## Pull requests

- Keep one change per pull request, with its tests and docs.
- If the change fixes a failure seedfall reports, in an example or in seedfall itself, paste the Markdown from `seedfall verify -md proof.md` into the description ([docs/verify.md](docs/verify.md)).
- Update the docs page that describes the behavior you changed, and the README if a feature or limitation changes. Do not describe anything the code does not do.
- Code has no comments; names and small functions carry the meaning.

## Commit messages

Commit subjects are one imperative sentence that says what changed and why, with no type prefix. Long subjects are fine when they list several related changes. From the history:

```
Simulate UDP with seeded latency, reordering and recorded udp_drop and udp_dup faults, add port 0 listens, CloseRead and CloseWrite, real peer addresses, sim.ListenPacket, and the memberlist and netcompat examples
Fix the timeline review findings: list data sent before accept in debug net, drop the next fault once a run is over, reject a negative seedfall_logs limit, ...
```

## License

seedfall is licensed under [Apache-2.0](LICENSE). By contributing, you agree that your contribution is licensed under the same terms.
