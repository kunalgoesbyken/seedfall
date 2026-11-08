# interleave

Two races between goroutines in one process, with no network.

**The shop.** 4 buyer goroutines share a stock counter and a sales ledger. `buy` checks that stock is left, writes a receipt, records the sale and decrements the counter, with no lock across the check and the update. If one buyer is preempted between the check and the update, two buyers sell the same item, and the run fails.

| Config | Result |
|---|---|
| `seedfall.json` | default scheduling: passes. Goroutines switch only when they block, so the race never opens |
| `interleave.json` | adds `"preempt": {"interleave": true}` (strategy `random`): fails on seed 1 (36 of the first 40 seeds), two buyers take the same item |
| `interleave.lock.json` | the same with `-lock`, a mutex across the check and the update: passes |

```sh
seedfall run -c interleave.json -seeds 1-40
seedfall replay -c interleave.json -seed 1
```

**The journal.** A writer goroutine appends records by reserving a slot (growing the slice) and then formatting the record into it, and signals a checkpointer every 10 records. The checkpointer copies the journal up to its reserved length instead of its committed length, after formatting a 256-field header. It copies an empty slot only if the writer stops inside its window and the checkpointer then formats its whole header and copies without being interrupted. `random` preempts every goroutine every few thousand calls, so the checkpointer never gets that far; `pct` preempts the writer once, at a seeded point, and lets the checkpointer run until it blocks.

| Config | Result, seeds 1-20 |
|---|---|
| `journal.json` | `"preempt": {"strategy": "pct", "depth": 2, "steps": 300000}`: fails on all 20, `checkpoint 1 copied entry 113, reserved but not yet written` on seed 1 |
| `journal.json -profile random` | strategy `random`: fails on 2 |
| `journal.json -profile none` | no preemption: passes |
| `journal.json -profile committed` | `pct` with `-committed`, copying up to the committed index: passes |

```sh
seedfall run -c journal.json -seeds 1-20
seedfall shrink -c journal.json -seed 1
```

The failing run records its one change point as a `preempt` fault, `preempt journal's running goroutine at counted call #259662`, and shrinking keeps it as the only fault. `steps` is 300,000, which puts the change point while the writer is still appending; the failing seed 1 had run 271,681 counted calls when it failed, and with `-committed` a full run reports about 740,000 on its `pct:` line.
