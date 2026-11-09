# BUGGIFY: faults inside your code

Every other seedfall fault comes from outside the process: crashes, torn writes, partitions. `sim.Buggify` lets you mark places in your own code where the simulator may force the rare path, such as a dropped reply, a retry, an error return, a minimal quorum or an extra delay. The idea comes from FoundationDB's `BUGGIFY`, which FoundationDB credits for many of its deepest bugs.

```go
v := c.apply(req)
if sim.Buggify("reply lost after apply") {
	return
}
reply(conn, v)
```

Outside the simulator `Buggify` always returns false, so the site costs one function call in production.

## Turning it on

Sites never fire unless the config asks for it:

```json
{ "faults": { "buggify": true } }
```

With `swarm` as well, each seed turns buggify on or off along with the other fault types.

## How a site decides

| Step | Rule |
|---|---|
| Enable | The first time a site is reached in a run, it is enabled with probability 0.25 and stays enabled or disabled for the rest of that run |
| Fire | Each time an enabled site is reached, it fires with probability 0.25 for `sim.Buggify(site)`, or `p` for `sim.BuggifyWithProb(site, p)` |
| Quiet | A site never fires during `sim.QuietFor`, during the recovery phase of `liveness` or the `finally` check, or, with `faults_after_setup`, before `sim.SetupComplete` |

A site is identified by its name, so keep names constant. Both decisions use random streams keyed by the seed and the site name (and, for firing, the node), so adding a site or reaching another site more often never changes when an existing site fires. Most seeds run with only some sites enabled, which explores combinations that firing every site everywhere would hide.

## Replay and shrink

Each firing is recorded as a fault with the node, the site and which hit it was, counted per node and site:

```
faults before the failure (1):
  465.631ms  buggify "reply lost after apply" fires on counter at hit #35
```

```json
{ "at": "465.631252ms", "kind": "buggify", "node": "counter", "site": "reply lost after apply", "until": "0s", "op": 35 }
```

When a schedule is replayed with `-faults`, a site fires exactly at the hits the schedule lists and nowhere else, so `seedfall shrink` removes firings like any other fault, and you can hand-edit a schedule to add or drop one. Replaying a schedule needs `"buggify": true` in the config, and with `swarm` it must be on for that seed: otherwise every site returns false. The trace shows when each site is first reached and whether it is enabled for the run, and every firing.

## Coverage

seedfall catalogs `sim.Buggify` and `sim.BuggifyWithProb` calls whose site is a string literal, and after a sweep with `buggify` on it prints how often each site was reached, enabled and fired:

```
buggify  seeds reached  seeds enabled  seeds fired  fired
                    20              6            6     88  reply lost after apply
                    20              5            5    110  slow apply
```

A site marked `never reached` (with its location) is code no seed ran; `never fired` means it ran but never fired: no seed enabled it, or its rate was too low for the hits it got, so sweep more seeds or raise its probability. Neither fails the sweep: pair a site with a `sim.Sometimes` on the path it opens when you want that path to be required. The same counts are in `buggify_sites` in `run -json` and in the MCP `seedfall_run` result.

## Structured randomness

`sim.Random()` returns a random `uint64` and `sim.RandomChoice(items)` returns one element of a slice (an empty slice fails the run, since there is nothing to return). Inside the simulator both read `crypto/rand`, which seedfall feeds from a stream keyed by the seed, the node and its restart count, so a choice replays with the seed. `RandomChoice` rejects out-of-range draws, so every element is equally likely. Outside the simulator they use `crypto/rand`.

```go
if sim.BuggifyWithProb("slow apply", 0.5) {
	time.Sleep(sim.RandomChoice([]time.Duration{time.Millisecond, 5 * time.Millisecond, 50 * time.Millisecond}))
}
```

The Antithesis SDK's `random.GetRandom` and `random.RandomChoice` read the same source, so Antithesis-instrumented code gets the same replayable choices with no changes. See [antithesis.md](antithesis.md).

## Example

[`examples/buggify`](../examples/buggify) is a counter service whose client retries an increment when the reply does not arrive. Without buggify nothing ever drops a reply and every seed passes; with it, `reply lost after apply` makes the retry apply twice and the sweep fails, and the fixed server, which remembers applied request ids, passes.

## Limits

- A site's name is its identity: two call sites with the same name share one enable decision and one hit count per node.
- Sites named by a non-literal string still fire and are recorded, but cannot be cataloged in advance.
- `seedfall cause` re-randomizes when sites fire after each branch point, not which sites are enabled, so a seed's enabled set is fixed for that seed.
