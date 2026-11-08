# Getting started

This walkthrough uses `examples/kvlog`, a small log-structured key-value server, to find a real durability bug, shrink it, fix it and prove the fix. It takes about five minutes.

## 1. Install

seedfall needs Go 1.25 or newer. It runs on Linux, macOS and Windows.

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
seedfall doctor
git clone https://github.com/kunalgoesbyken/seedfall
cd seedfall/examples/kvlog
```

`seedfall doctor` checks the toolchain and prints a fix for anything missing. Homebrew, Scoop, an install script, a Docker image and how to verify a release are in [install.md](install.md); those paths need a release newer than v0.2.0.

## 2. Look at the world

`seedfall.always.json` runs one server and one client. It extends `seedfall.json` and changes only the server's `-sync` flag; merged, it reads (abridged):

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
faults before the failure (2):
  427.268ms  crash server during disk op #56, restart at 761.268ms
     802ms  crash server, restart at 802ms
...
shrunk from 33 faults, 1m0s, 200 workload ops
         to 2 faults, 991ms, 63 workload ops
```

Shrink removed 31 faults, then made what was left simpler: the first crash restarts 30ms sooner, the second moved from 1.7s to 0.8s with no downtime, the run ends at 991ms instead of 60s, and the client, which sizes itself with [`sim.Budget`](properties.md#workload-size), sends 63 writes instead of 200.

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
- Open `.seedfall/failures/seedfall.always-seed-1/timeline.html` from the failing sweep for a diagram of every crash, partition and message, or step through the run with `seedfall debug -c seedfall.always.json -seed 1 -at 1.7s` ([timeline.md](timeline.md)).
- Each of these files is a few lines that extend `seedfall.json`. In your own project you can keep such variants as profiles in one file and run them with `-profile`, or try a fault preset with `-set faults=resilient` ([config.md](config.md)).

## Your own project

1. Make your nodes reachable: either switch `net.Listen` and `net.Dial` to `sim.Listen` and `sim.Dial`, or set `"net_overlay": true` to run unmodified code ([networking.md](networking.md)).
2. Write a workload: any Go `main` package that drives your system and checks invariants with `sim.Assert`, `sim.Always` and friends ([properties.md](properties.md)).
3. Describe the world in `seedfall.json` ([faults.md](faults.md)).
4. Sweep locally, then in CI ([ci.md](ci.md)), and from your coding agent ([agents.md](agents.md)).

Your code must build for `GOOS=wasip1`. cgo is not available. Dependencies that need `mmap` or `flock` work with `"compat_overlay": true`, through a shim for bbolt, pebble and badger or the generic shim for other callers of `Mmap` and `Flock`; `seedfall init` turns it on and says which dependency is covered ([compat.md](compat.md)).
