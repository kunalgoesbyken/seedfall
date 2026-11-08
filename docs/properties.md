# Properties, linearizability and determinism

## Failing a run

```go
sim.Fail("replica diverged")
sim.Assert(got == want, fmt.Sprintf("acknowledged write lost: %s=%s", key, want))
```

`Fail` and `Assert` stop the run with a message. Outside the simulator they panic, so the same code works in ordinary tests.

## Properties with coverage

A green sweep only means something if the dangerous paths actually ran. Properties are tracked across every seed and printed after a sweep:

```go
sim.Always(len(log) >= committed, "log never shrinks below the commit index", len(log), committed)
sim.Sometimes(tornTail, "recovery found a torn tail")
sim.Reachable("a node restored from a snapshot")
sim.Unreachable("two leaders in the same term", term)
```

| Function | Fails the seed when | Fails the sweep when |
|---|---|---|
| `Always(cond, msg, details...)` | `cond` is false | it was never reached |
| `AlwaysOrUnreachable(cond, msg, details...)` | `cond` is false | |
| `Unreachable(msg, details...)` | it is called | |
| `Sometimes(cond, msg, details...)` | | it was never reached, or `cond` was never true |
| `Reachable(msg, details...)` | | it was never reached |
| `Eventually(cond, msg, within)` | `cond()` is still false after `within` of virtual time, as a `liveness` failure | it was never reached |

```
property       seeds reached  seeds true
  sometimes             400         400  a crash interrupted the workload
  sometimes             400         172  recovery found a torn tail

ok  400 seeds passed (0 hit the time horizon) in 1m32s
```

A property is identified by its message, so keep messages constant and pass changing values as details. Details appear in the failure message but do not create a new property. A `Sometimes` that is never satisfied means your faults never reached that path: raise a fault rate, lengthen `duration`, or change the workload.

Properties are evaluated in any node, not only the workload. Outside the simulator, `Always`, `Unreachable` and `Eventually` panic on violation and the others do nothing.

### The property catalog

Before a sweep, seedfall scans the Go source of every node and the workload for calls to these functions with a constant message, so it knows about properties even if no seed ever executes them. A property that is never reached is reported with its location and fails the sweep:

```
property       seeds reached  seeds true
  reachable               0           0  compaction ran  <- never reached (node/main.go:12)
  always                  5           5  invariant holds

FAIL 1 properties were never reached or never true in 5 seeds
```

An `Always` that never runs proves nothing, so it has to be reached too. Calls whose message is not a string literal are still tracked once they run, but they cannot be cataloged in advance.

## Liveness: recovery and finally checks

Safety properties say nothing bad happens. Liveness properties say something good eventually happens, and they can only be checked once the faults stop. A cluster with a stuck leader, or a node that never reconnects after a network blip, passes every safety check: it just stops making progress, and a run that stops making progress only hits the time horizon.

Liveness mode adds a recovery phase to every run:

```json
"duration": "20s",
"liveness": { "within": "10s" }
```

For the first `duration`, faults fire as usual. Then the recovery phase starts: every remaining fault is suppressed, every partition, clog and slowdown ends, paused nodes resume, and crashed nodes restart at once instead of waiting out their downtime. From then on the workload has `within` of virtual time to finish. If it does not, the run fails with class `liveness`:

```
FAIL seed 1 at t=15s (2255 scheduler steps, 404ms real)
  liveness: client did not finish within 10s of the recovery phase starting at 5.000s

trace:
    4.172s  fault reset 1 connections of primary#1
    5.000s  recovery: the fault period ended; faults stop, partitions, clogs, slowdowns and pauses end, 0 crashed nodes restart now, everything must finish by 15.000s
   15.000s  FAIL  liveness: client did not finish within 10s of the recovery phase starting at 5.000s
```

That failure shrinks like any other: `seedfall shrink` cuts it to the faults that leave the system stuck, here a single connection reset. Liveness failures are grouped apart from safety failures in reports, as `liveness · client`.

### Eventually

`sim.Eventually` checks a condition that must become true, rather than one that must always hold:

```go
sim.Eventually(func() bool { return leader() != "" }, "a leader is elected", 5*time.Second)
```

It calls `cond` until it returns true, sleeping `within/100` of virtual time between calls, and returns true. If `cond` is still false after `within`, the run fails with class `liveness`, the property's message and `not true within 5s`. It is a property like the others: it is cataloged from the source, shows up in the coverage table as `eventually`, and fails the sweep if no seed ever reaches it. Outside the simulator it uses real time and panics when the condition never holds.

### Finally checks

A `finally` program runs once, after every workload has exited 0, the way Antithesis runs `finally_` commands:

```json
"workload": { "name": "client", "pkg": "./client" },
"finally": { "name": "check", "pkg": "./check" }
```

When the last workload exits, the recovery phase starts (if `liveness` has not started it already) and then the check starts. It never takes faults, it sees every node through a healed network, and the run passes when it exits 0. With `liveness`, it too must finish within `within` of the recovery phase. Its properties are cataloged and counted like the workload's, so this is the place for end-state checks: replicas converge, every acknowledged write is readable, nothing was applied twice.

```go
var p, b int
sim.Eventually(func() bool {
	p, b = count("primary:7000"), count("backup:7000")
	return p >= 0 && p == b
}, "primary and backup report the same count", 5*time.Second)
sim.Always(p == adds, "every add is applied exactly once", fmt.Sprintf("count is %d after %d adds", p, adds))
```

[`examples/liveness`](../examples/liveness) shows all three: a primary that never reconnects to its backup fails liveness mode, a server without request deduplication fails the finally check once the client is crashed mid-request, and the fixed pair passes.

### Quiet periods

`sim.QuietFor(d)`, called from any node, starts a quiet period from inside the program: it suppresses every fault for `d` of virtual time and heals any active partition, lifts every clog and ends every slowdown. Unlike the recovery phase, nodes that are already down still restart on schedule. The trace records each suppressed fault. `examples/raftkv/client` uses this to check that the cluster recovers after the workload.

```go
sim.QuietFor(10 * time.Second)
started := time.Now()
waitUntilServed()
sim.Always(time.Since(started) < 10*time.Second, "cluster serves reads again within the quiet period")
```

## Faults at your own code sites

`sim.Buggify("site")` returns true at seeded points when `faults.buggify` is on, so the simulator can force a retry, an error return or a delay inside your code. A quiet period stops sites from firing too. See [buggify.md](buggify.md).

## Waiting for setup

With `"faults_after_setup": true`, no faults fire until some node calls `sim.SetupComplete()`. Use it when bootstrapping, such as electing the first leader or creating a stream, is not what you want to test.

## Linearizability

Workloads can record key-value operations from any number of concurrent clients. When the workload finishes, seedfall checks the history with [Porcupine](https://github.com/anishathalye/porcupine).

```go
op := sim.Put(clientID, "k1", "v7")
reply, err := client.Put("k1", "v7")
switch {
case err != nil:          op.Unknown()
case reply == "redirect": op.Failed()
default:                  op.Ok("")
}

op = sim.Get(clientID, "k1")
value, err := client.Get("k1")
if err != nil {
	op.Unknown()
} else {
	op.Ok(value)
}
```

- `Ok(value)` means the operation took effect. For a read, `value` is what was read, with `""` meaning absent.
- `Unknown()` means it may or may not have taken effect, such as a timeout. Unknown writes may land any time after they start; unknown reads are dropped.
- `Failed()` means it definitely did not take effect, such as a redirect to the leader.

On a violation, seedfall writes an interactive timeline to `.seedfall/seed-N.history.html`.

## Determinism check

```sh
seedfall run -seeds 1-200 -check
```

Every seed runs twice, and the sweep fails with the first differing trace line if the two runs diverge. Run it in CI to make sure nothing in your program escapes the simulator.
