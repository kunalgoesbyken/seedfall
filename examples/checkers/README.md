# checkers

One in-memory store, driven by 4 concurrent clients, behind every history checker. Each operation reads the stored value, waits for a simulated storage round trip drawn from the seed, then writes the result back. With the lock held across the three steps the store is correct. `-racy` drops the lock, so two clients can read the same value and overwrite each other's result. For `txn`, `-racy` runs each micro-op atomically but not the whole transaction, the way a read-committed database would.

Each config names the model the history is checked against:

```json
"workload": { "name": "store", "pkg": "./store", "args": ["-model", "queue", "-racy"] },
"checker": { "model": "queue" }
```

| Config | Model | Result |
|---|---|---|
| `queue.json` | `queue` | passes 20 seeds |
| `queue.racy.json` | `queue` | fails on seed 1: two producers overwrite each other, two acknowledged items are never dequeued, and the run fails with `history of 240 operations is not linearizable under model queue` |
| `cas.json` | `cas` | passes 20 seeds: each client takes a lock with `cas("", me)`, holds it, and releases it with `cas(me, "")` |
| `cas.racy.json` | `cas` | fails on seed 1: two clients both swap the lock from free |
| `counter.json`, `set.json`, `append.json` | `counter`, `set`, `append` | pass 20 seeds |
| `counter.racy.json`, `set.racy.json`, `append.racy.json` | `counter`, `set`, `append` | fail on seed 1: a lost update, seen by a later read |
| `txn.json` | `list-append` | passes 20 seeds |
| `txn.racy.json` | `list-append` | fails on seed 1 as `isolation`: `history of 121 transactions is not serializable: G-single, G2, internal`, with each cycle explained in the trace |

```sh
seedfall run -c txn.racy.json -seeds 1-20
seedfall check -history .seedfall/seed-1.history.jsonl -model list-append
seedfall run -c queue.racy.json -seeds 1
seedfall check -history .seedfall/seed-1.history.jsonl -model queue   # writes .seedfall/seed-1.history.html
```
