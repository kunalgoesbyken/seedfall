# Operator mode

Operator mode lets an agent or a person act on a live simulated cluster, one step at a time, and scores the actions by replaying them. The cluster is an ordinary seedfall run: the same nodes, faults, disks and network. What changes is that something outside the cluster, the operator, can crash, restart, pause, partition and reconfigure nodes while the run is paused between two scheduler steps.

The reward does not come from the live episode. The operator's actions are logged with their virtual times, and `verify` replays that log from scratch on the episode's seed and on any other seeds the caller names. The reward is the share of those replays that pass. A caller that keeps some of the seeds secret gets a score the operator could not tune to.

![The agent sends steps to seedfall env and gets observations back. seedfall env applies actions to the simulated cluster between scheduler steps and logs them. verify replays the action log on the episode seed and on hidden seeds, and the reward is the share of replays that pass.](assets/diagrams/operator.svg "Figure 1. An episode. The agent acts through seedfall env, which applies each action between two scheduler steps and logs it with its virtual time. The score comes from verify, which replays that log from scratch on other seeds.")

## Actions

An action is `{"at": "2.5s", "kind": "restart", "node": "n2", "args": ["300ms"]}`. `at` is the virtual time the action applies at. A session sets it for you.

| Kind | Node | Args | Effect |
|---|---|---|---|
| `crash` | yes | none | Kills the node's process as a crash does, with the disk model of the config. It stays down until a `restart` |
| `restart` | yes | optional delay | Starts a node that is down after the delay. A node that is running is crashed first, then started after the delay |
| `pause` | yes | none | The node stops running until a `resume` |
| `resume` | yes | none | Ends a pause |
| `partition` | no | a duration, then the nodes of one side | Cuts the nodes of that side from the rest for the duration |
| `heal` | no | none | Ends partitions, clogs and slow nodes now |
| `slow` | yes | a factor of at least 1, then a duration | Stretches the node's latencies and timers |
| `clock_jump` | yes | a shift, such as `-50ms` | Moves the node's wall clock |
| `set_args` | yes | the node's whole new argument list | Replaces the node's arguments. They apply from its next start, so follow it with `restart` |
| `upgrade` | yes | none | Restarts the node on its `upgrade_to` build. The config must set `upgrade_to` for that node |
| `wait` | no | a duration | Does nothing and is logged. `advance` in a session is a `wait` followed by time passing |

`crash`, `restart` and `upgrade` need a node that takes faults, since a crash needs the disk model. Actions are not suppressed by a quiet period or by `faults_after_setup`, which only silence planned faults. Every action is traced as a line starting with `operator`, and enters the determinism hash.

Planned faults still happen around the operator's actions. A planned crash does nothing to a node that is already down, including one the operator stopped.

## When an action applies

An action with `at` T applies after every scheduler step at or before T has run, and before the first step after T. A session holds a virtual clock that starts at 0. `Advance(d)` moves the clock to T+d and runs every step at or before T+d, so everything an action does is applied between steps, never inside one. All actions at the same `at` apply together, in list order, before any step runs.

This rule is what makes a log replay exactly. A session applies its actions at the pause where the next step is later than the clock. A replay with the same actions in the schedule reaches the same point of the same run, with the same random streams in the same state. `Advance` must be positive, since an advance of zero would let steps run between two actions of the same time that a replay applies together.

## The Go API

`Runner.Operate(seed, schedule)` returns a `*Session` paused at virtual time 0. Planned faults come from the seed as in any run.

| Call | Does |
|---|---|
| `Do(op)` | Applies the action now. It is checked against the config first |
| `Advance(d)` | Logs a `wait`, then lets `d` of virtual time pass |
| `Observe()` | Node states and arguments, the trace lines since the last `Observe`, messages in flight and the client's operation counts. `Peek()` is the same without the log |
| `Ops()` | The action log, ready for `Schedule.Operator` |
| `Finish()` | Runs to the end and returns the `Result`: properties, linearizability, failure class and reason, hash |

`Schedule.Operator` carries the same list into `Replay`, `Run` and `Shrink`, and it is saved in schedule files under `operator`.

## The `seedfall env` protocol

`seedfall env [-j N]` reads one JSON object per line from stdin and writes one per line to stdout. Every response has `ok`, and an `error` when `ok` is false. A command that fails leaves the episode as it was.

| Request | Response |
|---|---|
| `{"cmd":"reset","config":"seedfall.json","seed":1}` | `observation`. Starts an episode, replacing the one in progress. `profile` and `set` work as the flags do. The compiled nodes are kept for the next `reset` of the same config |
| `{"cmd":"step","op":{"kind":"crash","node":"n1"}}` | `observation` after the action |
| `{"cmd":"step","advance":"2s"}` | `observation` after the time passed |
| `{"cmd":"observe"}` | `observation`, with the log lines since the last one |
| `{"cmd":"state"}` | `observation` without the log |
| `{"cmd":"finish"}` | `verdict`. Runs to the end |
| `{"cmd":"verify","ops":[...],"seeds":[2,3]}` | `verify`. Replays `ops` on the episode's seed and on each seed listed. Without `ops` it uses the episode's own log |

An observation has `at`, `over`, `failed` and `reason`, `nodes` (`state` of `runnable`, `blocked`, `paused`, `down`, `exited` or `not started`, plus `instance`, `until`, `args` and `starts`), `log`, `in_flight` and `client` (`invoked`, `ok`, `failed`, `unknown`, `open`, counted from the operations the workload records with `sim.Invoke`).

A verdict has `passed`, `finished`, `failure` and `reason`, `determinism_hash`, every `properties` entry with its `hits` and `true` counts, `linearizability` and `ops`, the action log. A replay passes when it did not fail and the workload finished. A run that reached the horizon with the workload still going does not pass.

`verify` returns `reward`, the share of seeds that passed, `passed`, and one entry per seed with `passed`, `failure`, `reason` and `determinism_hash`. The replays run in parallel, `-j` at a time.

`seedfall replay -c seedfall.json -seed N -ops ops.json` replays an action log, a JSON array of actions, and prints the trace and hash as any replay does. A failing replay saves its artifacts with the actions in `faults.json`, so the printed replay command reproduces it.

Over MCP, `seedfall_env_reset`, `seedfall_env_step`, `seedfall_env_observe`, `seedfall_env_finish` and `seedfall_env_verify` do the same for one episode held by the server. `seedfall_env_verify` takes only extra seeds and replays the episode's own log.

## The `opcluster` scenario

`examples/etcdraft/opcluster.json` is the 3-node etcd-io/raft key-value store of that example, run for an operator. Planned faults are crashes with restarts of 50 to 400ms, and partitions that isolate one node for 100 to 500ms, a crash about every 33s per node and a partition about every 33s. The clients run for 12s, the deadline, and keep running for up to 3s more while the nodes report their versions. Availability is measured over all of it.

The operator's job is to roll every node to version 2 by the 12s deadline. A node reports the version in its `-version` argument, which starts at 1, so only `set_args` and `restart` on each node changes it. Three clients write and read at about 100 operations a second, and the run fails if:

- a node already reports version 2 when the first 2s window ends, before availability is measured (`no node runs the new version before the clients are measured`);
- fewer than 40% of the clients' operations succeed in any 2s window after the first, or a window has none at all (`client availability meets the SLO in every window`);
- a node does not report version 2 within 3s of the deadline (`every node runs the new version by the deadline`);
- the history is not linearizable.

| Operator actions in `examples/etcdraft/ops` | Seeds 1 to 21 |
|---|---|
| `good.json`: set version 2 and restart n1 at 2s, n2 at 4.5s, n3 at 7s, each down for 300ms | 21 pass |
| `idle.json`: no actions | 21 fail on the version at the deadline |
| `restart-two.json`: n1 and n2 down together for 4s at 3s, n3 at 8s | 21 fail on availability |
| `restart-all.json`: all three down together for 4s at 3s | 21 fail on availability |
| `early-all.json`: all three restarted together at 100ms | 21 fail on the version before the first measured window |
| `deadline-all.json`: all three restarted together at 12s, down for 300ms | 21 pass |

`deadline-all.json` passing is a limit of this SLO, not of the check. Three nodes down for 300ms and the election after it cost about as much availability in a 2s window as a restart of the leader alone, so at 40% both pass. A stricter SLO fails the careful roll too: at 60% in 1s windows, `good.json` passes 9 of 21 seeds. An earlier version of the client stopped the clients at the deadline and then checked the versions with no one measuring, so any outage that started at the deadline was free. With the clients kept running, the same three restarts at 12s with 1.5s down fail on every seed.

`good.json` also passes on seeds 1 to 41. The first seed is the episode's, the rest are the extra seeds of `verify`:

```sh
cd examples/etcdraft
printf '{"cmd":"reset","config":"opcluster.json","seed":1}\n{"cmd":"verify","ops":%s,"seeds":[2,3,4,5]}\n' "$(cat ops/good.json)" | seedfall env
```

A session that acts on what it sees gets the same score. The check below drives one by hand on seed 7: advance 2s, then for each node in turn wait until no node is down, set the version, restart, advance 2.5s. Its `finish` hash, the hash of `verify` on seed 7 and the hash of `seedfall replay -seed 7 -ops` on the logged actions are all `c94bee8f92fb356f`.

## Measurements

Measured with `go test ./cmd/seedfall -bench Env` on `opcluster.json`, which has 3 raft nodes and 3 clients, on a Windows laptop with an Intel Core i5-9300H (4 cores, 8 threads), Go 1.27.1. The nodes are compiled and cached before each measurement. An episode here is a full run of about 15s of virtual time.

| Measure | Result |
|---|---|
| `reset`, a fresh episode from a cached config | 53ms, 80MB allocated |
| `step` with an action | 6µs |
| `step` with an advance of 100ms of virtual time | 38ms |
| Episodes a second at `-j 1` | 0.161 (6.2s each) |
| Episodes a second at `-j 8` | 0.776 (8 episodes in 10.3s) |
| Heap per open episode, 3s of virtual time in | 39.9MiB with 1 open, 39.0MiB with 8 open |
| Heap per open episode, run to its end | 29.5MiB with 1 open, 33.1MiB with 8 open |

The time goes to the raft nodes and clients, not to the environment: an action costs microseconds. Eight jobs give 4.8 times the throughput of one on this machine. Heap is what Go reports in use after a collection, measured as the growth from before the episodes opened, so it excludes the compiled nodes.

## Limits

- One episode runs at a time per `seedfall env` process and per MCP server. `reset` replaces it.
- The operator sees the trace, the node states and the client's operation counts, not the nodes' memory or disks.
- An action list replays against other seeds at the same virtual times. It cannot react to what a different seed does, so a good list leaves room for the planned faults.
- `restart` and `upgrade` are the same action for a node that has `upgrade_to`: as with a crash, the first restart runs the upgraded build.
- A session pauses only between scheduler steps, so an action never lands inside a guest's step.
- `verify` runs every seed to the end, so its cost is the number of seeds times the episode time above.
