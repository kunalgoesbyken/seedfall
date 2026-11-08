# memberlist

Three unmodified [hashicorp/memberlist](https://github.com/hashicorp/memberlist) nodes gossip over seedfall's simulated UDP and TCP. The nodes have no seedfall imports: `"net_overlay": true` routes `net.ListenUDP`, `net.ListenTCP` and the rest to the simulator, and `"compat_overlay": true` adds the route lookup `go-sockaddr` lacks on `wasip1`.

Each node binds its own IP, joins `n1` and keeps rejoining every 2s, because memberlist does not reconnect to a member it has marked dead once the gossip-to-the-dead window has passed. It serves its alive members at `/members`. The workload checks that the cluster forms, runs faults for 30s, then stops them and checks that every node sees every member again.

| Config | Result |
|---|---|
| `seedfall.json` | 5% `udp_loss`, 5% `udp_dup` and partitions: the cluster converges after the faults stop (20 seeds pass, `-check` clean) |
| `-profile strict` | Also asserts that no node ever drops a member: fails on a partition, and `shrink` cuts seed 2 from 19 faults to a partition and a single `udp_drop` |

```sh
seedfall run -seeds 1-20
seedfall run -profile strict -seeds 1-5
seedfall replay -seed 2 -profile strict -net-trace
```

The workload is shielded from faults with `"faults": false`, so an unreachable node means a real membership change and not a workload that lost its own connection.
