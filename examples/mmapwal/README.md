# mmapwal

A key-value store with a write-ahead log and a page file edited through a writable shared memory map. A put appends to `data/wal` and fsyncs, then updates a page in the mapping. Every 8 puts a checkpoint stores a CRC of each page in the first page, `msync`s the mapping, and truncates the log. It uses `github.com/blevesearch/mmap-go` and `github.com/gofrs/flock`, both through seedfall's generic `compat_overlay` shim. Uses the `kvlog` client.

The bug is in recovery. It checks each page against the checksum from the last checkpoint and treats a mismatch as corruption, assuming a page changed since the checkpoint can't be on disk. A kernel can write back any dirty page of a mapping whenever it likes, so a clean crash can leave a page with newer content and no matching checksum.

| Config | Server flags | Result |
|---|---|---|
| `seedfall.json` | none, crashes only | passes (20 seeds tested): every crash loses the mapping's unsynced pages, so the checksums always match |
| `-profile early` | none, `mmap_early_writeback` 0.5 | **fails**: a page written back before the crash no longer matches its checkpoint checksum. Shrinks to one crash and one early-written page |
| `-profile fixed` | `-lenient` (a mismatched page is left to the log), `mmap_early_writeback` 0.5 | passes (20 seeds tested) |

```sh
seedfall run    -c seedfall.json -profile early -seeds 1-5
seedfall shrink -c seedfall.json -profile early -seed 2
seedfall replay -c seedfall.json -profile early -seed 2 -faults .seedfall/seed-2.faults.json
```

See [Early writeback of mapped pages](../../docs/compat.md#early-writeback-of-mapped-pages).
