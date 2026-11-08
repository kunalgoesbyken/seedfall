# Mutation testing your checks

A sweep that passes is only meaningful if your workload would notice when a guarantee breaks. `seedfall mutate` tests that directly: it weakens the simulated disk, network or clock under your nodes, one guarantee at a time, and checks whether your sweep catches it.

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
| `fsync_corrupts` | File `fsync` makes the data durable with one bit flipped: the first byte of the last write it covers. Reads see the right data until a crash, then the flipped one |
| `rename_not_atomic` | A rename over an existing file is not durable until the next successful fsync of any file or directory on that disk. A crash before then loses both names, so neither the old nor the new version exists after the restart. A rename to a new name is unchanged |
| `truncate_not_durable` | `fsync` makes writes durable but skips truncations, including `O_TRUNC`. Reads see the shorter file until a crash, then the old length comes back with the old bytes past the new data |
| `net_drop_after_close` | When a connection is closed, the last chunk written to it that the peer has not read yet is dropped, so the peer sees end of file without it |
| `net_dup_chunk` | 5% of connection writes, chosen from the seed, arrive twice in a row |
| `monotonic_jump` | `clock_jump` faults move the monotonic clock along with the wall clock, so `time.Since` and timers see time run backwards or leap ahead. Needs `clock_jump_per_sec` |

Mutants need no changes to your code. Each one weakens only the nodes that take faults: the disk mutants their disks, the network mutants the data they receive, and `monotonic_jump` their clocks. The workload is affected only with `include_workload`, and the `finally` check never. A run with a mutant replays exactly like any other: the duplicated writes come from their own seeded stream.

Run some of them with `-only`:

```sh
seedfall mutate -c seedfall.json -seeds 1-20 -only net_dup_chunk,truncate_not_durable
```

`-only` picks among these runtime mutants and cannot be combined with `-app`. Over MCP the tool is `seedfall_mutate`, with `config`, optional `seeds` (default `1-100`) and `only`. It returns each mutant's `outcome`, the `killed_by_seed` and `reason` when killed, the `seeds_passed` and how many times the operation was `weakened`.

### The new mutants on `examples/ledger`

```
$ seedfall mutate -c seedfall.json -seeds 1-20
baseline: seeds 1-20 pass

  fsync_noop            killed     seed 1: client#1: ledger total covers every acknowledged deposit: {"left":4660,"right":5506,"unacked":1831}
  ...
  truncate_not_durable  SURVIVED   20 seeds passed although the operation was weakened 5 times
                        fsync makes writes durable but not truncations, so a crash brings back the old length and the bytes past it

  net_dup_chunk         killed     seed 18: client#1: ledger total holds no deposit that was never sent: {"left":8387,"right":8119}
                        a seeded 5% of connection writes are delivered twice, which TCP never does
  ...
1 mutants survived: your workload does not notice those guarantees disappearing
```

A duplicated `ADD` is applied twice and the upper bound on the total catches it. `truncate_not_durable` survives because the ledger cuts off a torn final entry with `Truncate` at every start, so a truncation that does not persist only brings back bytes that the next recovery drops again: a survivor you can explain as equivalent. In `examples/liveness`, `rename_not_atomic` survives 991 weakenings for a similar reason: when a crash loses the client's progress file, the client resends adds the servers have already deduplicated.

## Source mutants

The mutants above break the world under your program. `-app` breaks the program itself: seedfall parses the Go files of every node package (not the workload or the `finally` check), lists every place it can make one of three changes, picks `-mutants` of them (default 10) with the first seed of the range, and rebuilds the node once per mutant through a `go build -overlay`, so your files are never touched:

| Change | What it does |
|---|---|
| `flip_comparison` | Negates one comparison: `<` and `>=`, `>` and `<=`, `==` and `!=` |
| `skip_error_return` | `if x != nil { ...; return ... }` becomes `if false && x != nil`, so the early return never runs |
| `skip_sync` | `f.Sync()` becomes a call that returns `nil` without syncing |

```
$ seedfall mutate -c seedfall.fixed.json -seeds 1-10 -app -mutants 4
baseline: seeds 1-10 pass

  server/main.go:95     SURVIVED   10 seeds passed (or the mutated code never ran)
                        complete < len(data) -> complete >= len(data)

  server/main.go:97     killed     seed 1: server#2 crashed: module closed with exit_code(2)
                        err != nil -> err == nil

  server/main.go:157    killed     seed 1: client#1: acknowledged write lost: k61=v61, server now answers "NONE"
                        err != nil -> err == nil

  server/main.go:197    SURVIVED   10 seeds passed (or the mutated code never ran)
                        cmd[0] == "PUT" -> cmd[0] != "PUT"

2 mutants survived: your workload does not notice those guarantees disappearing
```

That is `examples/kvlog`. The survivor at line 197 is a real blind spot: with the comparison flipped, the server answers every `PUT` with an error, the client counts no write as acknowledged, and nothing asserts that writes are ever acknowledged, so the sweep still passes. The survivor at line 95 only changes the condition of a `sim.Sometimes`, which records coverage and never fails a seed.

seedfall cannot tell whether a mutated line ever ran, so a source mutant has no "not exercised" verdict: a survivor may be dead code. A mutant that does not compile (for example a `Sync` method with no error result) is reported as `did not build` and does not count as a survivor. The same seed range and `-mutants` count pick the same mutants every time. Each mutant costs one `go build` and one sweep, so start with a small count. `seedfall verify -app-mutants N` adds the same mutants to a proof ([verify.md](verify.md)), and over MCP `seedfall_mutate` takes `app` and `mutants`.

## Verdicts

- **killed**: some seed failed, so your workload notices this guarantee disappearing. That is the goal.
- **SURVIVED**: every seed passed even though the operation was weakened. Either your checks miss the broken guarantee (acknowledged data loss, a duplicated request, a timeout computed from a clock that ran backwards), or your faults never hit the window where it matters. Raise `crash_per_sec` or `crash_on_io`, lengthen the run, or strengthen the invariant. `seedfall mutate` exits non-zero when any mutant survives.
- **not exercised**: the weakened operation never ran, or had no effect in this configuration. For example, `dirsync_noop` without `strict_dirs`.

The baseline must pass first; otherwise mutants would be killed by the existing bug.
