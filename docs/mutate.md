# Mutation testing your durability checks

A sweep that passes is only meaningful if your workload would notice when durability breaks. `seedfall mutate` tests that directly: it weakens the simulated disk under your nodes, one guarantee at a time, and checks whether your sweep catches it.

```sh
seedfall mutate -c seedfall.always.json -seeds 1-30
```

```
baseline: seeds 1-30 pass

  fsync_noop     killed     seed 1: client#1: stream EVENTS lost after restart, 133 acknowledged messages gone
                 file fsync returns success without making anything durable

  dirsync_noop   not exercised (the operation never ran, or has no effect in this configuration)
                 directory fsync does nothing (matters with strict_dirs)

  osync_ignored  killed     seed 1: client#1: stream EVENTS lost after restart, 133 acknowledged messages gone
                 writes to O_SYNC and O_DSYNC files are no longer synchronous

every exercised mutant was killed
```

That run is NATS JetStream (`examples/natsjs`). Both guarantees NATS relies on are protected by the workload: when either one silently stops working, acknowledged messages are lost and the sweep fails.

## Mutants

| Mutant | What breaks |
|---|---|
| `fsync_noop` | File `fsync` reports success but makes nothing durable |
| `dirsync_noop` | Directory `fsync` does nothing. Only observable with `strict_dirs`, where directory entries need it to survive a crash |
| `osync_ignored` | Writes to files opened with `O_SYNC` or `O_DSYNC` stop being synchronous |

Mutants apply to nodes only, never to the workload, and need no changes to your code.

## Verdicts

- **killed**: some seed failed, so your workload notices this guarantee disappearing. That is the goal.
- **SURVIVED**: every seed passed even though the operation was weakened. Either your checks miss acknowledged data loss, or your faults never hit the window where it matters. Raise `crash_per_sec` or `crash_on_io`, lengthen the run, or strengthen the invariant. `seedfall mutate` exits non-zero when any mutant survives.
- **not exercised**: the weakened operation never ran, or had no effect in this configuration. For example, `dirsync_noop` without `strict_dirs`.

The baseline must pass first; otherwise mutants would be killed by the existing bug.
