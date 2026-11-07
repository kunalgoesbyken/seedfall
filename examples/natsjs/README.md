# natsjs

An embedded [nats-server](https://github.com/nats-io/nats-server) v2.15.2 with JetStream file storage. The server runs with `DontListen`, and each simulated connection is piped to `InProcessConn()`. The client connects through `nats.SetCustomDialer` with `sim.Dial`, creates a file-backed R1 stream, publishes 300 messages with retries across crashes, then verifies every acknowledged sequence.

| Config | Result |
|---|---|
| `seedfall.json` | default sync interval: fails on seed 1, the whole stream lost. This is NATS's documented behavior (fsync every 2 minutes), not a bug |
| `seedfall.always.json` | `SyncAlways`, crashes, torn writes and crashes mid-I/O: passes (150 seeds tested) |
| `seedfall.strict.json` | adds strict POSIX directories: fails on seed 1 (new stream directories and message blocks are never directory-fsynced) |
| `metasum.faults.json` with `seedfall.always.json -seed 40` | the [stream checksum finding](../../docs/findings/nats-stream-meta-checksum-crash.md): fails, 186 acknowledged messages gone |

```sh
seedfall replay -c seedfall.always.json -seed 40 -faults metasum.faults.json
```
