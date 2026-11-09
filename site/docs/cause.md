# Causality analysis

When a seed fails, you know it failed but not always which event decided it. `seedfall cause` replays the run from many points and shows when the failure became likely and which fault tipped it over. Use it after `shrink` when the short fault list still leaves you asking which fault matters.

`seedfall shrink` tells you which faults are *needed* for a failure. `seedfall cause` tells you *when the failure became likely*, and which event made it so.

```sh
seedfall cause -c seedfall.always.json -seed 1
```

```
seed 1 fails at t=2.891820635s: client#1: acknowledged write lost: k96=v96, server now answers "NONE"

chance of failing when everything after a moment is re-randomized (20 futures each):

         0s     5%  ██
  292.108ms     5%  ██                              <- after 292.108ms  partition [client] from the rest until 627.143ms
  722.955ms     0%
  757.268ms     0%
  757.268ms    75%  ███████████████████████         <- after 757.268ms  crash server during disk op #56, restart at 1.121388s
  1.084433s    75%  ███████████████████████
    1.7052s    65%  ████████████████████
    1.7052s   100%  ██████████████████████████████  <- after 1.7052s  crash server, restart at 1.942589s
  2.711082s   100%  ██████████████████████████████

biggest jump: 0% -> 75% at 757.268ms  <- after 757.268ms  crash server during disk op #56, restart at 1.121388s

440 runs in 24.296s
```

(Abridged: the full output has one row per moment.)

Read it from top to bottom. Before anything happens, a random future of this world fails 5% of the time. The partition changes nothing, so it is a bystander. The crash in the middle of disk operation #56 takes the odds from 0% to 75%: that is when the torn record lands on disk. The second crash makes the failure certain. Even before reading any code, you know which event to look at.

## How it works

For each moment *T*, seedfall replays the seed exactly up to *T*. At the first scheduler step at or after *T*, it re-keys every random stream: scheduling, network latency, disk tearing, I/O faults, what `random_get` hands each node (so `crypto/rand` and `sim.Random`, but not Go's runtime-seeded `math/rand`), and the remaining fault schedule. Then it lets the run continue into a new future. Running many such branches from the same moment estimates the probability of failing from that state.

This needs no snapshots. Because replay is exact, "the state at *T*" is reproduced by running the seed to *T*. That is the same property Antithesis's multiverse debugging relies on, with deterministic replay standing in for VM snapshots.

Moments are chosen evenly across the run (`-points`, default 16), plus just before and just after every fault. Each moment gets `-branches` futures (default 20), so the default costs a few hundred runs, all in parallel.

Branches are also the basis of [feedback-guided exploration](explore.md), which keeps branching from runs that reached something new.

## From an agent

The MCP server exposes the same analysis as `seedfall_cause`, returning the curve as JSON with the faults between consecutive moments. See [agents.md](agents.md).
