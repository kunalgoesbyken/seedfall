# Running Antithesis SDK code

Code instrumented with the [Antithesis Go SDK](https://github.com/antithesishq/antithesis-sdk-go) runs under seedfall unchanged. Assertions become seedfall properties, `lifecycle.SetupComplete` becomes seedfall's setup signal, and `random.GetRandom` draws from the seed.

`examples/ledger` has no seedfall imports at all. It uses plain `net` (through `net_overlay`) and only the Antithesis SDK:

```go
lifecycle.SetupComplete(map[string]any{"ledger": addr})
amount := int(random.GetRandom()%100) + 1
assert.Sometimes(unacked > 0, "a crash interrupted a deposit", nil)
assert.Always(total >= acked, "ledger total accounts for every acknowledged deposit", map[string]any{"total": total, "acked": acked})
```

```
$ seedfall run -c seedfall.json -seeds 1-100
property       seeds reached  seeds true
  sometimes             100          94  a crash interrupted a deposit
  always                100         100  ledger total accounts for every acknowledged deposit
  sometimes             100          23  recovery dropped a partial entry

ok  100 seeds passed (0 hit the time horizon) in 16.96s

$ seedfall run -c seedfall.nofsync.json -seeds 1-50
FAIL seed 1 at t=2.940789965s (618 scheduler steps, 607ms real)
  client#1: ledger total accounts for every acknowledged deposit: {"acked":5608,"total":4203,"unacked":1729}
```

## How it works

On `wasip1` the SDK has no native Antithesis library to talk to, so it writes each event as a JSON line to the file named by `ANTITHESIS_SDK_LOCAL_OUTPUT`. seedfall sets that variable for every node and serves the path as a virtual file. Each line is decoded as it is written:

| SDK call | seedfall property |
|---|---|
| `assert.Always` | `always`: fails the seed when false, fails the sweep if never reached |
| `assert.AlwaysOrUnreachable` | `always_or_unreachable`: fails the seed when false |
| `assert.Sometimes` | `sometimes`: fails the sweep if never true |
| `assert.Reachable` | `reachable`: fails the sweep if never reached |
| `assert.Unreachable` | `unreachable`: fails the seed when reached |
| `lifecycle.SetupComplete` | ends the setup phase when `faults_after_setup` is on |

The SDK's details map appears as JSON in the failure message. seedfall's source catalog also recognizes `assert.*` calls with a constant message, so Antithesis assertions that never run are reported too.

The SDK's numeric and grouped variants (`AlwaysGreaterThan`, `SometimesAll` and similar) are evaluated by the SDK and reported with their result, so they work at run time, but they are not cataloged in advance. `lifecycle.SendEvent` output is ignored.

`random.GetRandom` and `random.RandomChoice` read `crypto/rand`, which seedfall feeds from the seed, so they replay exactly and draw from the same source as `sim.Random` and `sim.RandomChoice`. Antithesis custom faults are commands its platform runs from outside your program and have no SDK call to map; mark the same spots with `sim.Buggify` instead ([buggify.md](buggify.md)).

## What you get compared with Antithesis

The same assertions and the same setup signal, run locally or in CI for free, with seed replay, shrinking, causality analysis and triage reports. What you don't get: Antithesis runs arbitrary containers in a deterministic hypervisor, while seedfall runs Go programs that build for `wasip1`.
