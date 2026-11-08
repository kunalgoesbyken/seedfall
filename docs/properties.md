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

### Numeric and grouped properties

The comparison and grouped forms match the [Antithesis SDK](antithesis.md) and say more than a plain boolean:

```go
sim.AlwaysGreaterThanOrEqualTo(len(log), committed, "log never shrinks below the commit index")
sim.SometimesGreaterThan(backlog, 100, "the queue backs up past 100")
sim.AlwaysSome(map[string]bool{"primary": p >= 0, "backup": b >= 0}, "some replica answers")
sim.SometimesAll(map[string]bool{"crashed": crashed, "recovered": recovered}, "a crashed node caught up")
```

| Function | Same as |
|---|---|
| `AlwaysGreaterThan(left, right, msg, details...)`, also `OrEqualTo` | `Always(left > right, ...)` (`>=`) |
| `AlwaysLessThan(left, right, msg, details...)`, also `OrEqualTo` | `Always(left < right, ...)` (`<=`) |
| `SometimesGreaterThan(left, right, msg, details...)`, also `OrEqualTo` | `Sometimes(left > right, ...)` (`>=`) |
| `SometimesLessThan(left, right, msg, details...)`, also `OrEqualTo` | `Sometimes(left < right, ...)` (`<=`) |
| `AlwaysSome(named, msg, details...)` | `Always` that at least one value in the `map[string]bool` is true |
| `SometimesAll(named, msg, details...)` | `Sometimes` that every value in the map is true |

`left` and `right` are any Go number type. On failure the details are JSON, `{"left":41,"right":40}` for the comparisons and the named booleans for the grouped forms, with any extra details under `"details"`.

For the comparisons seedfall also keeps a margin: `left - right` for `GreaterThan`, `right - left` for `LessThan`, so a positive margin is on the true side. An `always` comparison reports its smallest margin over every hit and seed, which is how close it came to failing; a `sometimes` comparison reports its largest, which is how close it came to being true. A margin of 0 on an `OrEqualTo` assertion means the boundary was hit exactly:

```
property       seeds reached  seeds true
  always                 20          20  no acknowledged add is lost  (smallest margin 0)
  sometimes              20           0  the queue backs up past 100  (largest margin -37)  <- never true (client/main.go:41)
```

The margin is also in `-json`, the MCP `seedfall_run` result (`margin`) and the HTML report.

### Events

```go
sim.Event("client resumed", map[string]int{"next add": done})
```

`sim.Event(name, details)` records a moment in the run without asserting anything. The details are encoded as JSON. Each event is a trace line (`event client resumed from client#2: {"next add":39}`), so it sits in order between the faults and the program output, and `seedfall_replay` returns the run's events as a list. Outside the simulator it does nothing. `lifecycle.SendEvent` from the Antithesis SDK is recorded the same way.

### The property catalog

Before a sweep, seedfall scans the Go source of every node and the workload for calls to these functions, including the numeric and grouped forms, with a constant message, so it knows about properties even if no seed ever executes them. A property that is never reached is reported with its location and fails the sweep:

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
sim.AlwaysLessThanOrEqualTo(p, adds, "no add is applied twice")
sim.AlwaysGreaterThanOrEqualTo(p, adds, "no acknowledged add is lost")
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

## Workload size

Wrap the number of operations your workload performs in `sim.Budget` so that `seedfall shrink` can make it smaller:

```go
writes := flag.Int("writes", 200, "number of writes")
flag.Parse()
n := sim.Budget(*writes)
for i := range n {
	// one operation
}
```

`sim.Budget(n)` returns `n` in normal runs and outside the simulator. When a shrunk schedule sets `ops`, it returns at most that many, and the trace says so: `client#1: sim.Budget capped at 63 of 200 operations`. Shrink only tries this when a node called `sim.Budget` in the failing run, and bisects the cap down to the fewest operations that still fail the same way. Use the returned value everywhere the count matters, including messages and checks such as "all `n` writes were acknowledged". `examples/kvlog/client` does this.

## Linearizability

Workloads can record operations from any number of concurrent clients. When the workload finishes, seedfall checks the history with [Porcupine](https://github.com/anishathalye/porcupine) against the model named in `seedfall.json` (`kv` when there is no `checker`).

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

- `Ok(output)` means the operation took effect. For a read, `output` is what was read, with `""` meaning absent.
- `Unknown()` means it may or may not have taken effect, such as a timeout. Unknown writes may land any time after they start; unknown reads are dropped.
- `Failed()` means it definitely did not take effect, such as a redirect to the leader.

On a violation, seedfall writes an interactive timeline to `.seedfall/seed-N.history.html`.

### Other models

`sim.Put` and `sim.Get` are shorthands for `sim.Invoke(client, f, key, value)`, which records any operation. Pick the model in `seedfall.json`:

```json
"checker": { "model": "queue" }
```

| Model | Operations: `sim.Invoke(client, f, key, value)`, then `op.Ok(output)` | Checked as |
|---|---|---|
| `kv` (default) | `put` with a string value; `get` with the string read as output, `""` for absent | A linearizable register per key |
| `cas` | `put` and `get` as in `kv`, plus `cas` with value `[]string{old, new}` and output `true` if it swapped, `false` if the value was not `old` | A linearizable compare-and-set register per key, for locks and leases |
| `append` | `append` with a string value; `read` with the whole list as output, a `[]string` | A linearizable append-only log per key |
| `queue` | `enqueue` with a string value; `dequeue` with the item taken as output, `""` when empty | A linearizable FIFO queue per key: no item lost, delivered twice or out of order |
| `set` | `add` with a string value; `read` with the members as output, a `[]string` in any order | A linearizable grow-only set per key |
| `counter` | `add` with an integer value; `read` with the integer read as output | A linearizable counter per key |
| `list-append` | `sim.Txn(client, mops...)`, see [transactions](#transactions) | Serializability, with Elle-style anomaly detection |

Every model checks each key on its own. An unknown `dequeue` stays in the history with any result allowed, since it may have removed an item; unknown pure reads (`get`, `read`) are dropped. An operation the model does not have, or an input or output of the wrong type, fails the run as an `assert` that names the operation, for example `history (model queue): operation "put" is not one of dequeue, enqueue`, so a typo cannot silently weaken the check.

```go
op := sim.Invoke(client, "cas", "leader-lock", []string{"", me})
swapped, err := locks.CompareAndSwap("leader-lock", "", me)
if err != nil {
	op.Unknown()
} else {
	op.Ok(swapped)
}
```

### Transactions

With `"checker": {"model": "list-append"}`, workloads record transactions over append-only lists, the workload Elle uses, and seedfall infers the dependencies between them:

```go
mops := []sim.Mop{sim.Read("x"), sim.Append("y", "c1-17")}
op := sim.Txn(client, mops...)
err := db.Txn(func(tx *Tx) {
	mops[0].Value = tx.List("x")
	tx.Append("y", "c1-17")
})
if err != nil {
	op.Unknown()
} else {
	op.Ok(mops)
}
```

`Ok` takes the same micro-ops back, with every read's `Value` set to the list it returned (a `[]string`). Appended values must be unique per key. seedfall takes each key's version order from its longest read, builds write-write, write-read and read-write edges between transactions, finds the strongly connected components with Tarjan's algorithm, and searches them breadth-first for one short cycle of each kind:

| Anomaly | Meaning |
|---|---|
| `G0` | A cycle of write-write edges: two transactions' appends interleave in different orders on different keys |
| `G1a` | A committed transaction read an append made by a transaction that `Failed()` |
| `G1b` | A transaction read a list ending in an intermediate append of another transaction, which appended to that key again later |
| `G1c` | A cycle of write-write and write-read edges with at least one write-read edge |
| `G-single` | A cycle with exactly one read-write edge, such as read skew |
| `G2` | A cycle that needs two or more read-write edges, such as write skew |
| `internal` | A transaction's read does not match its own earlier reads and appends |
| `incompatible-order` | Two reads of a key where neither is a prefix of the other, such as after a lost append |
| `duplicate-elements` | A read that contains the same element twice |
| `duplicate-append` | The same value appended to a key twice, which makes the history impossible to analyse |
| `garbage-read` | A read returned an element nobody appended |

A history with any of these fails as `isolation`, with one explained example of each kind in the trace:

```
G-single: T4 -rw z-> T0 -wr z-> T6 -ww y-> T4; T4 (client 4, ok) [r z ["4-0"], append y "4-1", r y ["3-0" "1-1" "4-1"]]; T0 ...
FAIL  history of 121 transactions is not serializable: G-single, G2, internal
```

`Tn` is the transaction's `id` in the history file. The check is serializability, not strict serializability: real-time order between transactions is not used. Transactions that returned `Unknown()` or never returned count as possibly committed: their appends are used once someone reads them, and their reads are ignored.

### History files

Every failing `run`, `replay` and `shrink`, and every passing `replay`, writes the recorded history to `.seedfall/seed-N.history.jsonl`, one event per line in Jepsen's invoke and completion shape:

```json
{"index":0,"type":"invoke","process":1,"f":"enqueue","key":"q","value":"1-0","time":8,"id":0}
{"index":4,"type":"ok","process":1,"f":"enqueue","key":"q","value":null,"time":648261,"id":0}
```

`type` is `invoke`, then `ok`, `info` (unknown) or `fail`; an operation that never returned has no completion line. `time` is virtual nanoseconds, and `id` pairs an invocation with its completion. A completion's `value` is the output for `ok` and the input otherwise. For `list-append`, `value` holds the micro-ops as `[f, key, value]` triples, the same layout as Elle's list-append micro-ops (seedfall's keys and values are strings). Over MCP, the failure view names the files as `history_file` and `timeline_file`.

Re-check a stored history, or check it against a different model, without running anything:

```sh
seedfall check -history .seedfall/seed-1.history.jsonl -model queue
```

It prints the verdict and every anomaly, writes the timeline next to the history file (or to `-timeline`) on a linearizability violation, and exits 1 when the history fails. The MCP tool is `seedfall_check`.

### Your own Porcupine model

For a data type none of the models cover, read a history file in Go and check it with your own [Porcupine](https://github.com/anishathalye/porcupine) model:

```go
f, _ := os.Open(".seedfall/seed-1.history.jsonl")
ops, err := engine.ReadHistory(f)
ok := porcupine.CheckOperations(myModel, engine.PorcupineOperations(ops))
```

`PorcupineOperations` drops failed operations and gives unknown and pending ones a return time after the end of the history. Each operation's `Input` and `Output` are its `engine.HistoryOp`, with the raw JSON `Value` and `Output` and the `Status`; your `Step` should accept any output when `Status` is not `ok`.

## Determinism check

```sh
seedfall run -seeds 1-200 -check
```

Every seed runs twice, and the sweep fails if the two runs diverge. Run it in CI to make sure nothing in your program escapes the simulator.

The comparison does not wait for a difference to reach the trace. Every run keeps a running hash over its host calls: clock reads, `random_get` lengths, sleeps and `poll_oneoff`, file opens, stats, reads, writes, truncates, syncs, renames and directory changes, network listens, dials, accepts, reads, writes and closes, and the `sim` property, event, history, `Buggify`, `SetupComplete`, `QuietFor` and `Budget` calls, each with its node instance, arguments and virtual time. The hash is saved every 4,096 calls. When the two runs differ, seedfall finds the first window of 4,096 calls where they differ, runs the seed twice more recording that window, and reports the first call that differs, in the form `host call N: <call in one run> vs <call in the other>, after <last common call>; host calls <window> (...)`. Each call reads as instance, call, target, argument and virtual time, for example `server#1 random_get(8) at 1.204s` or `server#1 fd_write data/log(37) at 1.204s`. Every `-check` failure is a `nondeterminism` failure.

If the extra runs do not reproduce the difference, the report names the window instead. If the host calls agree but the traces do not, it falls back to the first differing trace line.

The final hash is printed at the end of `seedfall replay` (`determinism hash 4c9ab008f72df649 over 9126 host calls`) and is the `determinism_hash` field of every `-json` seed line and of `seedfall_replay`. The hash covers only simulated values, so two machines that build the same guest binary should get the same hash for the same seed; comparing the field from CI runs on different machines checks that. A different Go toolchain or source tree produces a different binary and may change the hash.
