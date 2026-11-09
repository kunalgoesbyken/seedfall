# Running Antithesis SDK code

Antithesis is a commercial testing platform with its own Go SDK, a library for writing checks inside your program. If your code already uses that SDK, seedfall runs it unchanged and turns each SDK call into a seedfall check. Read this page if you are coming from Antithesis or share code with a team that uses it.

Code instrumented with the [Antithesis Go SDK](https://github.com/antithesishq/antithesis-sdk-go) runs under seedfall unchanged. Assertions become seedfall properties, `lifecycle.SetupComplete` becomes seedfall's setup signal, and `random.GetRandom` draws from the seed.

`examples/ledger` has no seedfall imports at all. It uses plain `net` (through `net_overlay`) and only the Antithesis SDK:

```go
lifecycle.SetupComplete(map[string]any{"ledger": addr})
amount := int(random.GetRandom()%100) + 1
assert.Sometimes(unacked > 0, "a crash interrupted a deposit", nil)
lifecycle.SendEvent("deposits finished", map[string]any{"total": total, "acked": acked, "unacked": unacked})
assert.AlwaysGreaterThanOrEqualTo(total, acked, "ledger total covers every acknowledged deposit", map[string]any{"unacked": unacked})
assert.AlwaysLessThanOrEqualTo(total, acked+unacked, "ledger total holds no deposit that was never sent", nil)
assert.SometimesAll([]assert.NamedBool{{First: "interrupted", Second: unacked > 0}, {First: "kept", Second: total > acked}}, "an unacknowledged deposit survived a crash", nil)
```

```
$ seedfall run -c seedfall.json -seeds 1-20
property       seeds reached  seeds true
  sometimes              20          18  a crash interrupted a deposit
  sometimes              20           2  an unacknowledged deposit survived a crash
  always                 20          20  ledger total covers every acknowledged deposit  (smallest margin 0)
  always                 20          20  ledger total holds no deposit that was never sent  (smallest margin 0)
  sometimes              20           5  recovery dropped a partial entry

ok  20 seeds passed (0 hit the time horizon) in 908ms

$ seedfall run -c seedfall.json -profile nofsync -seeds 1-20
FAIL seed 1 at t=2.940793302s (618 scheduler steps, 336ms real)
  client#1: ledger total covers every acknowledged deposit: {"left":4203,"right":5608,"unacked":1729}
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
| `assert.AlwaysGreaterThan`, `AlwaysGreaterThanOrEqualTo`, `AlwaysLessThan`, `AlwaysLessThanOrEqualTo` | `always`, with the smallest margin in the property table |
| `assert.SometimesGreaterThan`, `SometimesGreaterThanOrEqualTo`, `SometimesLessThan`, `SometimesLessThanOrEqualTo` | `sometimes`, with the largest margin in the property table |
| `assert.AlwaysSome` | `always`: at least one named boolean is true |
| `assert.SometimesAll` | `sometimes`: every named boolean is true |
| `lifecycle.SetupComplete` | ends the setup phase when `faults_after_setup` is on |
| `lifecycle.SendEvent` | an [event](properties.md#events): a trace line with the details as JSON, returned by `seedfall_replay` |

The SDK's details map appears as JSON in the failure message; the numeric variants add `left` and `right`, and the grouped variants add each named boolean. seedfall's source catalog recognizes every `assert.*` call above with a constant message, so Antithesis assertions that never run are reported too.

The margin of a numeric assertion comes from the SDK's own guidance records. Like Antithesis, the SDK only reports a new value when it gets closer to failing (for `Always`) or closer to true (for `Sometimes`), so the smallest and largest margins are exact. The SDK also reports each assertion only the first time it passes and the first time it fails in a process, so the number of hits is lower than with `sim` assertions; seeds reached and seeds true are unaffected.

`sim` has the same variants with the same names ([properties.md](properties.md#numeric-and-grouped-properties)), and `sim.Event` matches `lifecycle.SendEvent`.

`random.GetRandom` and `random.RandomChoice` read `crypto/rand`, which seedfall feeds from the seed, so they replay exactly and draw from the same source as `sim.Random` and `sim.RandomChoice`. Antithesis custom faults are commands its platform runs from outside your program and have no SDK call to map; mark the same spots with `sim.Buggify` instead ([buggify.md](buggify.md)).

## What you get compared with Antithesis

The same assertions and the same setup signal, run locally or in CI for free, with seed replay, shrinking, causality analysis and triage reports. What you don't get: Antithesis runs arbitrary containers in a deterministic hypervisor, while seedfall runs Go programs that build for `wasip1`.
