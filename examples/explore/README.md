# explore

A contrived workload that shows why a sweep stops being enough. A client draws a random byte 2000 times, climbing one level on a lucky draw (about 1 in 10) and dropping to zero on any other. Six lucky draws in a row, which a random seed almost never produces, open the vault and fail an assertion. Each level is a `sim.AlwaysOrUnreachable` so reaching it is a signal the exploration can branch from, without a plain sweep treating a level it never reached as a problem. The bug is synthetic on purpose: the point is the search, not the bug.

The workload draws with `crypto/rand`. A branch re-keys that source; it would not change Go's runtime-seeded `math/rand`, see [explore.md](../../docs/explore.md#what-re-keying-does-not-reach).

| Command | Result |
|---|---|
| `seedfall run -seeds 1-20` | passes: no seed gets past level 4 |
| `seedfall explore -seeds 1-8 -rounds 30 -batch 8` | fails after 32 runs, at seed 6 with two chained branches: `client#1: the vault opened after 6 consecutive lucky draws` |

The failure replays, shrinks and inspects from the `faults.json` in its artifacts folder, like any other.
