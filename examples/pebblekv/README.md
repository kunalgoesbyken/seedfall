# pebblekv

A key-value server on [cockroachdb/pebble](https://github.com/cockroachdb/pebble) v2.1.7, unmodified. pebble doesn't build for `wasip1` (its `vfs` package has no file system for it), so the configs set `"compat_overlay": true` ([compat](../../docs/compat.md)). The server acknowledges `SET` only after `db.Set` with `pebble.Sync` returns. The client accepts any value at least as new as the last acknowledged one.

Every config sets `strict_dirs`. pebble's manifest marker is an empty file made durable by a directory fsync, which seedfall's default disk model doesn't credit ([details](../../docs/compat.md#pebble-and-directory-fsyncs)).

| Config | Result |
|---|---|
| `seedfall.json` | `pebble.Sync`, crashes and partitions: passes (200 seeds tested) |
| `seedfall.torn.json` | adds torn writes and crashes mid-I/O: passes (200 seeds tested) |
| `seedfall.nosync.json` | `pebble.NoSync`: fails on seed 1, as expected (negative control) |
