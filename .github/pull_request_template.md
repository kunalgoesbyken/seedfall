## What and why

<!-- What this changes and the problem it solves. Link the issue it closes. -->

## How it was tested

- [ ] `go test ./...`, or `go test -short ./...` with the reason the guest tests were skipped
- [ ] `gofmt -l .` prints nothing and `go vet -stdmethods=false ./...` passes
- [ ] Golden files rewritten with `-update` were reviewed, if any changed
- [ ] The `scripts/regress.sh` checks for every example this touches pass (run them by hand; the full script takes hours)
- [ ] The docs describe the new behavior, and `bash scripts/site-llms.sh` was rerun if `docs/` changed

## Verify proof

<!--
If this fixes a failure that seedfall reports, in an example or in seedfall itself, paste the Markdown written by:

  seedfall verify -c <config> -seed <N> -faults .seedfall/seed-<N>.faults.json -mutate -md proof.md

It replays the failing schedule, sweeps fresh seeds for 95% confidence, checks determinism and, with -mutate, the mutants. See docs/verify.md.
Delete this section if it does not apply.
-->
