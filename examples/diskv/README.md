# diskv

A key-value server on [peterbourgon/diskv](https://github.com/peterbourgon/diskv) v3.0.1, one file per key. At its strongest settings, the server writes with `WriteStream(key, r, true)`, which fsyncs the file, and sets `TempDir`, which writes to a temporary file and renames it into place, so an overwrite is atomic. It acknowledges `SET` only after the write returns. The client overwrites 40 keys 300 times and accepts any value at least as new as the last acknowledged one.

| Config | Result |
|---|---|
| `seedfall.json` | `TempDir` + sync, with crashes, partitions, torn writes and crashes mid-I/O: passes (300 seeds tested) |
| `seedfall.nosync.json` | without sync: fails on seed 3, as expected (negative control) |
| `seedfall.inplace.json` | sync, without `TempDir`: fails on seed 2, as documented (an in-place overwrite truncates the old value first, so it is not atomic) |
| `seedfall.strict.json` | `TempDir` + sync under strict POSIX directories: fails on seed 1, an acknowledged overwrite reverts to an older value |

Root cause and verified fix: [docs/findings/diskv-rename-dir-fsync.md](../../docs/findings/diskv-rename-dir-fsync.md).
