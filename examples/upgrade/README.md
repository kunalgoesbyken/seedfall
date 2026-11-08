# upgrade

A ledger service that ships a new on-disk format, and a client that checks nothing is lost across the upgrade. It shows three config features working together: a node that starts from an existing disk, files placed on a node's disk from the host, and a package swapped in when the node restarts.

- `v1/` stores deposits as `alice 120` lines. `v2/` stores them as `alice=120` under a `LEDGER v2` header and migrates a v1 file the first time it starts.
- `image/data/ledger` is a v1 ledger that already holds eight deposits. `"disk": "image"` copies it onto the node's disk before the first start, durable and synced, as if v1 had written it in an earlier life.
- `"upgrade_to": "./v2"` builds `v2/` too. The node starts as v1, and every restart from the first crash on runs v2 against whatever v1 left on disk. The trace marks the moment: `start ledger#2, upgraded to ./v2`.
- `"files"` puts `fixtures/atomic.conf` at `etc/ledger.conf`, where v2 reads its migration mode, and gives the client a copy of the seeded ledger at `expected` so it can check every seeded balance.

The client deposits into five new accounts, checks the eight seeded accounts every ten deposits, and checks each new account against what was acknowledged and sent at the end. Its `Sometimes` property fails the sweep if no seed ever ran v2, so a green run cannot come from the upgrade never happening. Faults come from the `durability` preset: crashes, crashes in the middle of disk operations, torn and reordered writes, and strict directory durability.

One `seedfall.json` holds every variant as a profile:

| Run | Result |
|---|---|
| `seedfall run -seeds 1-100` | migration writes `data/ledger.tmp`, fsyncs it, renames it over the ledger and fsyncs `data`: passes 100 seeds |
| `seedfall run -profile nomigrate -seeds 1-5` | `etc/ledger.conf` says `migrate = none`, so v2 ignores the v1 lines: fails seed 1 right after the upgrade, `seeded account alice holds 0, want 100` |
| `seedfall run -profile inplace -seeds 1-30` | v2 rewrites the ledger in place. A crash in the middle of that rewrite leaves the new header and a torn part of the converted lines, and the next start takes the file as already migrated: fails seed 21, `new4 holds 24 after 30 acknowledged and 30 sent deposits` |

The `inplace` failure is the classic upgrade bug that only shows up when a real old data file meets the new version under a crash, which is why seeding the disk matters: without it the first run of v2 would always see an empty disk.

To reuse a failing state as the starting point of new runs, export it with `seedfall inspect -out` and point `disk` at the node's directory:

```sh
seedfall inspect -profile inplace -seed 21 -out state
seedfall run -set nodes.ledger.disk=state/ledger -seeds 1-20
```

The trace then starts with `ledger's disk starts with 2 durable files, 1135 bytes`, the exact bytes seed 21 left behind. Here that run fails at once, because the node starts as v1 again and cannot read the v2 ledger the failure left.

See [docs/config.md](../../docs/config.md).
