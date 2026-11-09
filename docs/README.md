# Documentation

Every page in `docs/`, grouped by what you want to do: learn seedfall from scratch, get a specific task done, look something up, or understand how it works.

## Tutorials

- [Getting started](getting-started.md): find, shrink, fix and prove a real bug in five minutes.

## How-to guides

- [Installing seedfall](install.md): Homebrew, Scoop, the install script, Docker and `go install`, and verifying a release.
- [Networking](networking.md): make nodes reachable with `sim.Listen` and `sim.Dial`, or with no code changes through `net_overlay`.
- [Storage engines that need mmap or flock](compat.md): run bbolt, pebble, badger and other `mmap` or `flock` users through `compat_overlay`.
- [Config files, profiles and seeded disks](config.md): `extends`, `profiles`, `-set`, fault presets, seeded disks and upgrades.
- [Feedback-guided exploration](explore.md): find failures that need two rare events in one run.
- [Causality analysis](cause.md): find the fault that made a seed fail.
- [Inspecting the disk at the moment of failure](inspect.md): durable, pending and torn bytes on every node.
- [Timelines, logs and time-travel debugging](timeline.md): space-time diagrams, filtered traces and `seedfall debug`.
- [Mutation testing your checks](mutate.md): check that your properties notice broken durability.
- [Proving a fix with `seedfall verify`](verify.md): the proof block for a pull request, and pinned regressions.
- [BUGGIFY: faults inside your code](buggify.md): mark fault sites in your own code.
- [Running seedfall in CI](ci.md): the GitHub Action, machine-readable output, time budgets and `go test`.
- [Using seedfall from AI coding agents](agents.md): the MCP server, skills and Stop hook.
- [Running Antithesis SDK code](antithesis.md): programs instrumented with the Antithesis Go SDK.
- [Use your Gostman collections as workloads](gostman.md): API checks as the workload.
- [Troubleshooting](troubleshooting.md): the errors seedfall prints, their causes and fixes.

## Reference

- [Configuration and faults](faults.md): every `seedfall.json` field, fault, network fault, disk fault and file system model.
- [Properties, linearizability and determinism](properties.md): the `sim` assertions, liveness, history checkers and `-check`.
- [Triage reports](reports.md): findings, classification, failure artifacts, notifications and utilization.
- [Troubleshooting](troubleshooting.md): the error catalog, keyed by exact message.

## Explanation

- [How seedfall works](how-it-works.md): the simulated clock, preemption, determinism, the disk and network models, checking, shrinking and speed.
- [FAQ](faq.md): seedfall compared with Antithesis, gosim, `testing/synctest`, `-race` and Jepsen, why `wasip1`, and what it cannot test.
- [Testing seedfall itself](testing.md): what the unit tests and the example suite prove, and the CI workflows.
- [Findings](findings/README.md): bugs seedfall found in open-source projects, with replay commands and verified fixes.

## Project

- [Contributing](../CONTRIBUTING.md): development setup, tests, examples, compat shims, findings and commit style.
- [Roadmap](../ROADMAP.md): what is planned and what is not done yet.
- [Security policy](../SECURITY.md) and [code of conduct](../CODE_OF_CONDUCT.md).
