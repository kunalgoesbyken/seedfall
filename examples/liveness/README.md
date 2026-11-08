# liveness

A primary that replicates every add to a backup before acknowledging it, a client that sends 40 adds and saves its progress to its own disk, and a `finally` check that runs after the client. Two bugs hide here, and neither breaks a safety property while faults are firing:

- **The primary never reconnects.** Without `-redial`, once the replication connection to the backup breaks, through a reset, a partition timeout or a backup crash, the primary refuses every add from then on. Nothing is lost and nothing is wrong. The system just stops.
- **Retries are not idempotent.** The client derives each request id from `sim.Node()` and its saved progress, so after a crash it resends the add that was in flight. Without `-dedupe`, the servers apply that add a second time.

The fixed config passes with every fault on, the client included:

```json
"workload": { "name": "client", "pkg": "./client" },
"finally": { "name": "check", "pkg": "./check" },
"duration": "5s",
"liveness": { "within": "10s" },
"faults": { "include_workload": true, "crash_per_sec": 0.3, "pause_per_sec": 0.3, "partition_per_sec": 0.3, "reset_per_sec": 0.5, ... }
```

For 5s of virtual time the faults fire, the client included. Then the recovery phase stops them, heals the network and restarts crashed nodes, and the client must finish within 10s. Once it does, `check` asks both servers for their count:

```go
sim.Eventually(func() bool {
	p, b = count(*primary), count(*backup)
	return p >= 0 && p == b
}, "primary and backup report the same count", *within)
sim.AlwaysLessThanOrEqualTo(p, *adds, "no add is applied twice")
sim.AlwaysGreaterThanOrEqualTo(p, *adds, "no acknowledged add is lost")
```

| Config | Result |
|---|---|
| `seedfall.json` | `-dedupe -redial`, liveness mode, every fault on the servers and the client: passes 20 seeds, each run twice with `-check`. The client resumed after a crash in 15 of them |
| `noredial.json` | the same without `-redial`: fails on seed 1, 14 of the first 20 seeds, `liveness: client did not finish within 10s of the recovery phase starting at 5.000s`. `seedfall shrink` cuts seed 1 from 12 faults to one connection reset, saved as `stuck.faults.json` |
| `noredial.noliveness.json` | the same bug without `liveness`: every seed passes, all 20 at the time horizon, because a stuck run and a slow run look the same when the run just ends. Only the property catalog notices, because the `finally` check never ran and its three properties were never reached |
| `nodedupe.json` | servers with `"faults": false` and no `-dedupe`, and only the client crashing: fails on seed 2, 11 of the first 20 seeds, `no add is applied twice: {"left":41,"right":40}`. It shrinks to one client crash, saved as `client-crash.faults.json` |
| `stuck.faults.json` | the single reset. It fails `noredial.json` and passes `seedfall.json` |
| `client-crash.faults.json` | the single client crash. It fails `nodedupe.json` and passes `seedfall.json` |

```sh
seedfall run    -c seedfall.json -seeds 1-20
seedfall run    -c noredial.json -seeds 1-20
seedfall shrink -c noredial.json -seed 1
seedfall replay -c noredial.json -seed 1 -faults stuck.faults.json
seedfall run    -c nodedupe.json -seeds 1-20
seedfall replay -c seedfall.json -seed 2 -faults client-crash.faults.json
```

The shrunk liveness failure:

```
FAIL seed 1 at t=15s (2304 scheduler steps, 303ms real)
  liveness: client did not finish within 10s of the recovery phase starting at 5.000s

faults before the failure (1):
  4.172215s  reset every connection of primary

trace:
    0.000s  start primary#1
    0.000s  start backup#1
    0.000s  start client#1
    4.172s  fault reset 1 connections of primary#1
    5.000s  recovery: the fault period ended; faults stop, partitions, clogs, slowdowns and pauses end, 0 crashed nodes restart now, everything must finish by 15.000s
   15.000s  FAIL  liveness: client did not finish within 10s of the recovery phase starting at 5.000s
```

The shrunk double apply:

```
FAIL seed 2 at t=1.596559982s (328 scheduler steps, 125ms real)
  check#1: no add is applied twice: {"left":41,"right":40}

faults before the failure (1):
  1.411618s  crash client, restart at 1.553865s

trace:
    0.000s  start primary#1
    0.000s  start backup#1
    0.000s  start client#1
    1.412s  fault crash client#1, restart at 1.554s
    1.554s  start client#2
    1.554s  event client resumed from client#2: {"next add":39}
    1.579s  client#2| all 40 adds acknowledged
    1.579s  workload finished, the finally check runs next
    1.579s  recovery: the workload finished; faults stop, partitions, clogs, slowdowns and pauses end, 0 crashed nodes restart now, everything must finish by 11.579s
    1.579s  start check#1
    1.597s  FAIL  check#1: no add is applied twice: {"left":41,"right":40}

determinism hash c4effe4a6634e9b4 over 3507 host calls
```
