# Documentation

seedfall runs your Go system in a simulated world, breaks it on purpose (crashes, lost messages, half-written files), and lets you replay any failure exactly. These pages are grouped by what you want to do. If you are new, read the first one and stop there for now.

## Start here

- [Getting started](getting-started.md): find, shrink, fix and prove a real bug in about five minutes, using an example that is included.
- [Installing seedfall](install.md): every way to install it, and how to check the install worked.
- [FAQ](faq.md): short answers, such as how seedfall differs from other tools and what it cannot test.
- [Troubleshooting](troubleshooting.md): look up an error message and see how to fix it.

## Find a bug

- [Networking](networking.md): let your nodes talk to each other inside the simulation.
- [Properties and checks](properties.md): write the statements your system must keep true, such as "an acknowledged write is never lost".
- [Storage engines that need mmap or flock](compat.md): test databases such as bbolt, pebble and badger.
- [Config files, profiles and seeded disks](config.md): keep several test setups in one file and start nodes from an existing disk.
- [Feedback-guided exploration](explore.md): hunt for bugs that need two rare events in one run.
- [BUGGIFY](buggify.md): mark places in your own code where seedfall may force a rare path.
- [Mutation testing your checks](mutate.md): find out whether your checks would notice a real bug.
- [Running Antithesis SDK code](antithesis.md): run programs that already use the Antithesis Go SDK.
- [Use your Gostman collections as workloads](gostman.md): reuse API checks you already wrote.

## Understand a failure

- [Timelines, logs and time-travel debugging](timeline.md): see a failing run as a diagram, a filtered log or a step-by-step debugger.
- [Causality analysis](cause.md): find which fault actually made a seed fail.
- [Inspecting the disk at the moment of failure](inspect.md): see which bytes were saved and which a crash would lose.
- [Proving a fix with `seedfall verify`](verify.md): show that a fix works, and pin the bug so it cannot come back.
- [Triage reports](reports.md): group many failing seeds into a short list of bugs.

## Use in CI

- [Running seedfall in CI](ci.md): the GitHub Action, machine-readable output, time limits and `go test`.
- [Using seedfall from AI coding agents](agents.md): the MCP server and skills that let an agent run seedfall for you.

## Reference

- [Configuration and faults](faults.md): every `seedfall.json` field and every fault seedfall can inject.
- [Properties, linearizability and determinism](properties.md): the full `sim` API for checks.
- [Triage reports](reports.md): findings, classification, failure artifacts, notifications and utilization.
- [Troubleshooting](troubleshooting.md): the error catalog, keyed by exact message.
- [How seedfall works](how-it-works.md): the simulated clock, the disk and network models, checking, shrinking and speed.
- [Testing seedfall itself](testing.md): what the project's own tests prove.
- [Findings](findings/README.md): bugs seedfall found in open-source projects, with replay commands.

## About the project

- [Contributing](../CONTRIBUTING.md): development setup, tests and commit style.
- [Roadmap](../ROADMAP.md): what is planned and what is not done yet.
- [Security policy](../SECURITY.md) and [code of conduct](../CODE_OF_CONDUCT.md).
