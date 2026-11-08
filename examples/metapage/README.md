# metapage

A key-value server that keeps `key=value` records after a 4 KiB meta page in `data/kv.db`. The meta page holds two checksummed slots, each with a sequence number and the length of the committed records, like LMDB's meta pages. A put writes the record, then writes the next slot, then fsyncs once, then acknowledges. It uses the `kvlog` client.

Torn writes cannot break it: a crash keeps an in-order prefix of the pending writes, so the new slot only survives if the record before it did, and a torn slot fails its checksum and recovery falls back to the other one. Real page caches write dirty pages back in any order, though. With `reorder_writes`, the meta page can reach the disk while the record page it commits does not.

| Config | Server flags | Result |
|---|---|---|
| `seedfall.json` | none, `torn_writes` | passes (100 seeds tested). In 93 of them recovery finds a record the slot does not commit, so the crash window is covered |
| `-profile reorder` | none, `torn_writes` and `reorder_writes` (the default `ext4_ordered` `fs_model`) | **fails** on seed 26: the meta slot, an overwrite that ext4 can write back at any time, commits a record the crash tore |
| `-profile fixed` | `-sync-body` (fsync the record before writing the slot) | passes (100 seeds tested) |

```sh
seedfall run     -c seedfall.json -profile reorder -seeds 1-30
seedfall shrink  -c seedfall.json -profile reorder -seed 26
seedfall inspect -c seedfall.json -profile reorder -seed 26 -faults reorder.faults.json -node server
```

```
    kv.db                                                4673       4673  durable
      ^ torn by the crash at t=1.529695124s: kept 1 of a 10-byte write at offset 4672
      ^ the crash at t=1.529695124s wrote back pages out of order: 2 of 2 pending writes reached the disk
        #1 partial  1 of a 10-byte write at offset 4672
        #2 kept     20-byte write at offset 20
```
