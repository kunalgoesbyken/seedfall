# etcdraft

A 3-node key-value store on [etcd-io/raft](https://github.com/etcd-io/raft) v3, in the style of etcd's `raftexample`. Storage is a plain-file WAL with a CRC per record and torn-tail truncation; every `Ready` is persisted and fsynced before its messages are sent, and snapshots are written through a temporary file, fsynced, renamed and the directory fsynced. 4 concurrent clients are checked for linearizability.

| Config | Result |
|---|---|
| `seedfall.json` | passes (100 seeds tested), linearizable, with elections, PreVote, CheckQuorum and snapshot transfer exercised |
| `-profile strict` | passes under strict POSIX directories (100 seeds tested) |
| `-profile nofsync` | `-no-fsync` skips the WAL fsync: fails on seed 2. The node lost its unsynced log but kept a newer snapshot, and raft panics on restart |

## Operator mode

`opcluster.json` runs the same cluster for an operator, a person or an agent that acts on it while it runs ([docs/operator-env.md](../../docs/operator-env.md)). Crashes with restarts and partitions that isolate one node come from the seed. The operator has to roll every node onto version 2 by the 12s deadline, while the clients keep at least 40% of their operations succeeding in every 2s window after the first, and the history stays linearizable. A node reports the version in its `-version` argument, so only `set_args` followed by `restart` on each node gets it there.

| Operator actions | Result |
|---|---|
| `ops/good.json` restarts n1, n2 and n3 one at a time, 2.5s apart, each down for 300ms | passes |
| `ops/idle.json` does nothing | fails: `every node runs the new version by the deadline` |
| `ops/restart-two.json` restarts n1 and n2 together for 4s | fails: `client availability meets the SLO in every window` |
| `ops/restart-all.json` restarts all three together for 4s | fails: `client availability meets the SLO in every window` |

```sh
seedfall replay -c opcluster.json -seed 1 -ops ops/good.json
printf '{"cmd":"reset","config":"opcluster.json","seed":1}\n{"cmd":"verify","ops":%s,"seeds":[2,3,4,5]}\n' "$(cat ops/good.json)" | seedfall env
```
