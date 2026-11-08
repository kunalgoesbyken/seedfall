# etcdraft

A 3-node key-value store on [etcd-io/raft](https://github.com/etcd-io/raft) v3, in the style of etcd's `raftexample`. Storage is a plain-file WAL with a CRC per record and torn-tail truncation; every `Ready` is persisted and fsynced before its messages are sent, and snapshots are written through a temporary file, fsynced, renamed and the directory fsynced. 4 concurrent clients are checked for linearizability.

| Config | Result |
|---|---|
| `seedfall.json` | passes (100 seeds tested), linearizable, with elections, PreVote, CheckQuorum and snapshot transfer exercised |
| `-profile strict` | passes under strict POSIX directories (100 seeds tested) |
| `-profile nofsync` | `-no-fsync` skips the WAL fsync: fails on seed 2. The node lost its unsynced log but kept a newer snapshot, and raft panics on restart |
