# Getting started

This walkthrough uses `examples/kvlog`, a small log-structured key-value server, to find a real durability bug, shrink it, fix it and prove the fix. It takes about five minutes.

## 1. Install

seedfall needs Go 1.25 or newer. It runs on Linux, macOS and Windows.

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
git clone https://github.com/kunalgoesbyken/seedfall
cd seedfall/examples/kvlog
```

## 2. Look at the world

`seedfall.always.json` runs one server and one client (abridged):

```json
{
  "nodes": [{ "name": "server", "pkg": "./server", "args": ["-sync", "always"] }],
  "workload": { "name": "client", "pkg": "./client", "args": ["-writes", "200"] },
  "faults": { "crash_per_sec": 0.5, "torn_writes": true, "crash_on_io": 0.005 }
}
```

The server appends `key=value` lines to `data/kv.log` and fsyncs before every acknowledgement. The client writes 200 keys, then checks that every acknowledged key is still there, using `sim.Assert`. The only seedfall-specific code in the server is `sim.Listen`.

## 3. Sweep

```sh
seedfall run -c seedfall.always.json -seeds 1-100
```

```
FAIL seed 1 at t=2.891820635s (737 scheduler steps, 445ms real)
  client#1: acknowledged write lost: k96=v96, server now answers "NONE"
```

Even with an fsync before every acknowledgement, data is lost.

## 4. Shrink

```sh
seedfall shrink -c seedfall.always.json -seed 1
```

```
faults before the failure (2):        # shrunk from 33
  427.268ms  crash server during disk op #56, restart at 791.388ms
   1.7052s  crash server, restart at 1.942589s
```

The first crash lands in the middle of an append and leaves a torn, half-written line. On restart, recovery skips the bad line but keeps appending after it, so the next record is glued onto the garbage. The second crash forces another recovery, which can no longer parse that record.

`seedfall replay -c seedfall.always.json -seed 1` prints the full trace, and adding `-faults .seedfall/seed-1.faults.json` replays exactly the minimal schedule.

## 5. Fix and prove it

The fix is to truncate the torn tail during recovery. The server already has it behind `-repair`, and `seedfall.fixed.json` enables it:

```sh
seedfall run -c seedfall.fixed.json -seeds 1-1000
```

```
property       seeds reached  seeds true
  sometimes            1000         994  a crash interrupted the workload
  sometimes            1000         406  recovery found a torn tail

ok  1000 seeds passed (0 hit the time horizon) in 1m28.94s
```

The property table shows that the recovery path really ran: hundreds of seeds produced a torn tail, and every one was recovered correctly.

## 6. Go further

- `seedfall.fsyncgate.json` adds `fsync_eio`, and the "fixed" server fails again: a failed fsync leaves a hole in the durable log. `seedfall.rewind.json` adds the fix.
- `seedfall.strict.json` adds strict POSIX directory durability.

## Your own project

1. Make your nodes reachable: either switch `net.Listen` and `net.Dial` to `sim.Listen` and `sim.Dial`, or set `"net_overlay": true` to run unmodified code ([networking.md](networking.md)).
2. Write a workload: any Go `main` package that drives your system and checks invariants with `sim.Assert`, `sim.Always` and friends ([properties.md](properties.md)).
3. Describe the world in `seedfall.json` ([faults.md](faults.md)).
4. Sweep locally, then in CI ([ci.md](ci.md)), and from your coding agent ([agents.md](agents.md)).

Your code must build for `GOOS=wasip1`. cgo is not available. Dependencies that need `mmap` or `flock`, such as bbolt, pebble and badger, work with `"compat_overlay": true` ([compat.md](compat.md)).
