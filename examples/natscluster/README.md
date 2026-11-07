# natscluster

A 3-node [nats-server](https://github.com/nats-io/nats-server) v2.15.2 cluster with JetStream file storage and `sync_always`, run unmodified through `net_overlay`: client and route listeners are plain `net.Listen`, and the servers find each other at `n1:6222`, `n2:6222` and `n3:6222`. The client connects to all three servers, creates a file-backed R3 stream (replicated with NATS's own Raft), publishes 300 messages with retries across crashes and partitions, then stops the faults and must read every acknowledged message back at its sequence within 30 seconds.

| Config | Result |
|---|---|
| `seedfall.json` | crashes, partitions, torn writes, crashes during disk I/O: passes (200 seeds tested), with publishes interrupted in 198 of them |
| `seedfall.pause.json` | crashes, torn writes, process pauses and wall-clock jumps: passes (100 seeds tested) |
| `seedfall.strict.json` | adds strict POSIX directories: fails on seed 3: acknowledged messages are gone and their sequence numbers reused (`seq 1 holds "m45", but m0 was acknowledged there`). Three crashes are enough, one node at a time, never two down together (`strict.faults.json`). This is the cluster-wide consequence of the directory fsyncs missing from the file store, already described in the strict-POSIX part of the [stream checksum finding](../../docs/findings/nats-stream-meta-checksum-crash.md): a restarted node can come back without its copy of the stream, and Raft cannot survive replicas that forget acknowledged entries. The same schedule passes without strict directories, and a server with that finding's directory fixes passes it and 60 strict seeds |
| `seedfall.r1.nosync.json` | an R1 stream without `sync_always`: fails on seed 1, the stream lost on restart. The negative control |

```sh
seedfall replay -c seedfall.strict.json -seed 3 -faults strict.faults.json
```
