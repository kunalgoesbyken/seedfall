# symswap

A key-value server that keeps each version of its data in a file `data/gen.N` and publishes it by pointing a symlink, `data/current`, at the new file. A put writes and fsyncs the new generation, creates a temporary symlink and renames it over `current`, then fsyncs the directory. Uses the `kvlog` client.

The bug is in the order of the directory operations. The new generation's directory entry is not fsynced before the rename that publishes it. On a journaling file system the journal commits directory operations in order, so the rename can only survive with the create before it. On ext2 or btrfs, where directory operations can persist out of order, `current` can point at a file that does not exist.

| Config | Server flags | Result |
|---|---|---|
| `seedfall.json` | none, `reorder_writes` with the default `ext4_ordered` | passes (20 seeds tested) |
| `-profile dirs` | none, `fs_model` `dir_reorder` | **fails**: `current` points at a generation that does not exist after the crash |
| `-profile fixed` | `-sync-dir` (fsync the directory before the rename), `dir_reorder` | passes (20 seeds tested) |

```sh
seedfall run -c seedfall.json -seeds 1-20
seedfall run -c seedfall.json -profile dirs -seeds 1-20
seedfall run -c seedfall.json -profile fixed -seeds 1-20
```

See [File system models](../../docs/faults.md#file-system-models).
