# buggify

A counter service and a client that sends 40 increments, retrying each one until it gets a reply, then checks that the counter is 40. The bug is a classic: the retry is not idempotent, so an increment whose reply was lost after it was applied is applied twice. No outside fault reaches that window here, because nothing ever drops a reply. Two `sim.Buggify` sites in `counter/main.go` open it from inside the code:

```go
if sim.BuggifyWithProb("slow apply", 0.5) {
	time.Sleep(sim.RandomChoice(delays))
}
v := c.inc(id)
if sim.Buggify("reply lost after apply") {
	return
}
```

| Config | Result |
|---|---|
| `seedfall.json` | `"buggify": true`: fails on seed 2 (6 of the first 20 seeds), `counter is 51 after 40 increments` |
| `seedfall.fixed.json` | the same with `-dedupe`, which remembers applied request ids: passes 20 seeds, and the client's `Sometimes` shows the retry path ran in 6 of them |
| `seedfall.off.json` | buggify off: no assertion fails, but the sweep fails because `an increment was retried after its reply was lost` was never true. The retry path never ran, so a green run would have proved nothing |
| `lost-reply.faults.json` | seed 2 shrunk to one firing. It fails `seedfall.json` and passes `seedfall.fixed.json` |

```sh
seedfall run    -c seedfall.json -seeds 1-20
seedfall shrink -c seedfall.json -seed 2
seedfall replay -c seedfall.json -seed 2 -faults lost-reply.faults.json
seedfall run    -c seedfall.fixed.json -seeds 1-20
```

After a sweep with buggify on, seedfall lists every site it found in the source with how often it was reached, enabled and fired:

```
buggify  seeds reached  seeds enabled  seeds fired  fired
                    20              6            6     88  reply lost after apply
                    20              5            5    110  slow apply
```

See [docs/buggify.md](../../docs/buggify.md).
