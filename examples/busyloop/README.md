# busyloop

Call-free loops under `"preempt": {"loops": true}`. One process: a leader takes a 30ms lease, checksums 4 MiB in a loop with no function calls, and commits a write, ten times with a seeded pause between writes. It checks the lease before the checksum but not after.

Without `loops`, the checksum costs no virtual time: a loop that makes no call is never counted, so the node runs it in one go and the clock barely moves. With `loops`, its 4,194,304 iterations fill a time slice, the node is parked for 10ms of virtual time, and a write that started near the end of the lease commits after it.

| Config | Result, seeds 1-10 |
|---|---|
| `seedfall.json` | `loops` on: fails on every seed, `a write commits while the lease is held: map[late:8.000045ms write:1]` on seed 1 |
| `seedfall.json -profile calls` | `loops` off: passes, the checksum takes no virtual time |
| `seedfall.json -profile recheck` | `loops` on, `-recheck` checks the lease again just before committing: passes |
| `seedfall.json -profile spin` | `-spin` waits for the replicator goroutine with `for !ready {}` instead of a channel: fails as `liveness` at 100ms on seed 1 |
| `seedfall.json -profile spin-calls` | the same spin with `loops` off: fails as `timeout` once the 3s real-time `watchdog` fires |

The spin never ends in either case: on `wasip1` the Go runtime switches goroutines only at a function entry, so the goroutine that would set `ready` never runs. The difference is how it fails. With `loops` the spinning node parks every time slice, virtual time moves, and liveness mode fails it at the same virtual time on every run. Without `loops` the node never yields and only the real-time watchdog stops it, which depends on the host.

```sh
seedfall run -seeds 1-10
seedfall run -profile spin -seeds 1
```

Loop counting costs guest CPU, and a long spin under it takes real time: the `spin` profile shortens `duration` and `liveness.within` to 50ms each so the run stays short.
