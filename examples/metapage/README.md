# metapage

A key-value server that keeps `key=value` records after a 4 KiB meta page in `data/kv.db`. The meta page holds two checksummed slots, each with a sequence number and the length of the committed records, like LMDB's meta pages. A put writes the record, then writes the next slot, then fsyncs once, then acknowledges. It uses the `kvlog` client.

Torn writes cannot break it: a crash keeps an in-order prefix of the pending writes, so the new slot only survives if the record before it did, and a torn slot fails its checksum and recovery falls back to the other one. Real page caches write dirty pages back in any order, though. With `reorder_writes`, the meta page can reach the disk while the record page it commits does not.

| Config | Server flags | Result |
|---|---|---|
| `seedfall.json` | none, `torn_writes` | passes (100 seeds tested). In 93 of them recovery finds a record the slot does not commit, so the crash window is covered |
| `seedfall.reorder.json` | none, `torn_writes` and `reorder_writes` | **fails** on seed 16: the meta slot commits a record that never reached the disk. Shrinks to 1 fault |
| `seedfall.fixed.json` | `-sync-body` (fsync the record before writing the slot) | passes (100 seeds tested) |

```sh
seedfall run     -c seedfall.reorder.json -seeds 1-30
seedfall shrink  -c seedfall.reorder.json -seed 16
seedfall inspect -c seedfall.reorder.json -seed 16 -faults reorder.faults.json -node server
```

```
    kv.db                                                4182       4182  durable
      ^ the crash at t=218.308324ms wrote back pages out of order: 1 of 2 pending writes reached the disk
        #1 lost     8-byte write at offset 4182
        #2 kept     20-byte write at offset 0
```
