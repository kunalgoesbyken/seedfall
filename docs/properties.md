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
| `Always(cond, msg, details...)` | `cond` is false | |
| `Unreachable(msg, details...)` | it is called | |
| `Sometimes(cond, msg, details...)` | | `cond` was never true in any seed |
| `Reachable(msg, details...)` | | (reported only once reached) |

```
property       seeds reached  seeds true
  sometimes             400         400  a crash interrupted the workload
  sometimes             400         172  recovery found a torn tail

ok  400 seeds passed (0 hit the time horizon) in 1m32s
```

A property is identified by its message, so keep messages constant and pass changing values as details. Details appear in the failure message but do not create a new property. A `Sometimes` that is never satisfied means your faults never reached that path: raise a fault rate, lengthen `duration`, or change the workload.

Properties are evaluated in any node, not only the workload. Outside the simulator, `Always` and `Unreachable` panic on violation and the others do nothing.

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
