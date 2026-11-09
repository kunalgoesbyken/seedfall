# Getting started

This walkthrough takes about five minutes. You will run seedfall on a small example server that loses data after a crash. You will find the bug, cut the failure down to its simplest form, fix it and prove the fix. The example is `examples/kvlog`, a key-value server that appends every write to a log file.

A few words you will see:

- A **node** is one program in your system, such as a server. seedfall runs each node as its own process inside a simulation.
- A **workload** is a program that uses your system and checks the answers. It plays the part of a client.
- A **fault** is something that goes wrong on purpose: a crash, a lost message, a half-written file.
- A **seed** is a number that decides everything random in one run, including when the faults happen. The same seed always gives the same run, so any failure can be replayed.

## 1. Install

You need Go 1.25 or newer. seedfall runs on Linux, macOS and Windows.

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
seedfall doctor
git clone https://github.com/kunalgoesbyken/seedfall
cd seedfall/examples/kvlog
```

`seedfall doctor` checks your setup and tells you how to fix anything that is missing. Other ways to install (Homebrew, Scoop, a script, Docker) are in [install.md](install.md). Those need a release newer than v0.2.0.

## 2. Look at the example

The file `seedfall.always.json` describes the test: one server, one client and the faults to inject. It extends `seedfall.json` and changes one setting, the server's `-sync` flag. Merged, it reads (shortened):

```json
{
  "nodes": [{ "name": "server", "pkg": "./server", "args": ["-sync", "always"] }],
  "workload": { "name": "client", "pkg": "./client", "args": ["-writes", "200"] },
  "faults": { "crash_per_sec": 0.5, "torn_writes": true, "crash_on_io": 0.005 }
}
```

What this means:

- The server appends `key=value` lines to `data/kv.log`. Before it tells a client "done", it calls **fsync**. Fsync is the operating system call that forces data onto the disk. Without it, a crash can lose data that is only in memory.
- The client writes 200 keys. Then it checks that every key the server acknowledged is still there. The check is a `sim.Assert` call.
- The faults crash the server about every two seconds and allow **torn writes**, where a crash leaves a write only half saved.
- The server's only seedfall-specific code is `sim.Listen`, which opens its network port.

## 3. Run a sweep

A sweep runs the test once per seed. Try seeds 1 to 100:

```sh
seedfall run -c seedfall.always.json -seeds 1-100
```

```
FAIL seed 1 at t=2.891820635s (737 scheduler steps, 445ms real)
  client#1: acknowledged write lost: k96=v96, server now answers "NONE"
```

Seed 1 fails. The server said it saved a key, and after the crashes the key was gone, even though the server uses fsync before every acknowledgement. That is a real bug.

## 4. Shrink the failure

Seed 1 had 33 faults, and most do not matter. `shrink` removes faults and simplifies what is left, while the failure keeps happening:

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

Two faults are enough. Shrink also made them simpler: the first crash restarts 30ms sooner, the second moved from 1.7s to 0.8s, the run ends at 991ms instead of 60s, and the client sends 63 writes instead of 200. (The client can shrink its own size because it uses [`sim.Budget`](properties.md#workload-size).)

Here is the bug in plain words:

1. The first crash lands in the middle of an append. The last line in the log is left half written.
2. On restart, recovery skips the bad line but keeps appending after it. The next record is glued onto the garbage.
3. The second crash forces another recovery, which can no longer read that record.

To see the whole run, use `seedfall replay -c seedfall.always.json -seed 1`. To replay only the two faults that matter, add `-faults .seedfall/seed-1.faults.json`.

## 5. Fix it and prove it

The fix is to cut off the half-written tail during recovery. The server already has this behind a flag called `-repair`, and `seedfall.fixed.json` turns it on:

```sh
seedfall run -c seedfall.fixed.json -seeds 1-1000
```

```
property       seeds reached  seeds true
  sometimes            1000         994  a crash interrupted the workload
  sometimes            1000         406  recovery found a torn tail

ok  1000 seeds passed (0 hit the time horizon) in 1m28.94s
```

All 1000 seeds pass. The table below the result matters too. It shows that the risky paths really ran: in 406 seeds recovery met a torn tail, and each one was handled correctly. A passing sweep that never reached the risky code would prove little.

## 6. Go further

- `seedfall.fsyncgate.json` makes fsync itself fail (`fsync_eio`). The "fixed" server breaks again, because a failed fsync leaves a hole in the log. `seedfall.rewind.json` adds the fix.
- `seedfall.strict.json` also loses directory changes that were never synced, as POSIX allows.
- Open `.seedfall/failures/seedfall.always-seed-1/timeline.html` after the failing sweep for a diagram of every crash and message. Or step through the run with `seedfall debug -c seedfall.always.json -seed 1 -at 1.7s` ([timeline.md](timeline.md)).
- Each of these config files is a few lines that extend `seedfall.json`. In your own project you can keep such variants as profiles in one file and pick one with `-profile`, or try a ready-made fault preset with `-set faults=resilient` ([config.md](config.md)).

## Use it on your own project

1. Make your nodes reachable inside the simulation. Either change `net.Listen` and `net.Dial` to `sim.Listen` and `sim.Dial`, or set `"net_overlay": true` to run your code unchanged ([networking.md](networking.md)).
2. Write a workload. It is any Go `main` package that uses your system and checks the results with `sim.Assert`, `sim.Always` and similar calls ([properties.md](properties.md)).
3. Describe the test in `seedfall.json` ([faults.md](faults.md)).
4. Run sweeps on your machine, then in CI ([ci.md](ci.md)), and from your coding agent ([agents.md](agents.md)).

`seedfall init` can write a first config and workload for you.

One requirement: your code must build for `GOOS=wasip1`. This is Go's target for WebAssembly System Interface, a small sandbox that lets seedfall control time, randomness and the disk. In practice it means no cgo (Go code that calls C). Libraries that need `mmap` or `flock` (operating system calls for mapping files and locking them) work when you set `"compat_overlay": true`; `seedfall init` turns it on and tells you which dependency is covered ([compat.md](compat.md)).
