# interleave

A shop with one process and no network: 4 buyer goroutines share a stock counter and a sales ledger. `buy` checks that stock is left, writes a receipt, records the sale and decrements the counter, with no lock across the check and the update. If one buyer is preempted between the check and the update, two buyers sell the same item, and the run fails.

| Config | Result |
|---|---|
| `seedfall.json` | default scheduling: passes. Goroutines switch only when they block, so the race never opens |
| `interleave.json` | adds `"preempt": {"interleave": true}`: fails on seed 1 (36 of the first 40 seeds), two buyers take the same item |
| `interleave.lock.json` | the same with `-lock`, a mutex across the check and the update: passes |

```sh
seedfall run -c interleave.json -seeds 1-40
seedfall replay -c interleave.json -seed 1
```
