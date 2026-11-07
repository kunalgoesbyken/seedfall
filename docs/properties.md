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
| `Unreachable(msg, details...)` | it is called | |
| `Sometimes(cond, msg, details...)` | | it was never reached, or `cond` was never true |
| `Reachable(msg, details...)` | | it was never reached |

```
property       seeds reached  seeds true
  sometimes             400         400  a crash interrupted the workload
  sometimes             400         172  recovery found a torn tail

ok  400 seeds passed (0 hit the time horizon) in 1m32s
```

A property is identified by its message, so keep messages constant and pass changing values as details. Details appear in the failure message but do not create a new property. A `Sometimes` that is never satisfied means your faults never reached that path: raise a fault rate, lengthen `duration`, or change the workload.

Properties are evaluated in any node, not only the workload. Outside the simulator, `Always` and `Unreachable` panic on violation and the others do nothing.

### The property catalog

Before a sweep, seedfall scans the Go source of every node and the workload for calls to these functions with a constant message, so it knows about properties even if no seed ever executes them. A property that is never reached is reported with its location and fails the sweep:

```
property       seeds reached  seeds true
  reachable               0           0  compaction ran  <- never reached (node/main.go:12)
  always                  5           5  invariant holds

FAIL 1 properties were never reached or never true in 5 seeds
```

An `Always` that never runs proves nothing, so it has to be reached too. Calls whose message is not a string literal are still tracked once they run, but they cannot be cataloged in advance.

## Liveness: quiet periods

Safety properties say nothing bad happens. Liveness properties say something good eventually happens, and they can only be checked once the faults stop.

```go
sim.QuietFor(10 * time.Second)
started := time.Now()
waitUntilServed()
sim.Always(time.Since(started) < 10*time.Second, "cluster serves reads again within the quiet period")
```

`sim.QuietFor(d)` suppresses every fault for `d` of virtual time and heals any active partition. Nodes that are already down still restart on schedule. The trace records each suppressed fault. `examples/raftkv/client` uses this to check that the cluster recovers after the workload.

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
