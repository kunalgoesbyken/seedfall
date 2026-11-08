# Storage engines that need mmap or flock

Some storage libraries don't build or run for `GOOS=wasip1` because they call `mmap`, `flock` or other syscalls that WASI doesn't have. With `compat_overlay`, seedfall gives them `wasip1` versions at build time, either a shim for that exact library or the [generic `mmap` and `flock` shim](#the-generic-mmap-and-flock-shim), so you test the library you ship, unmodified:

```json
{ "compat_overlay": true }
```

| Library | Without `compat_overlay` | With it | Example |
|---|---|---|---|
| `go.etcd.io/bbolt` v1.5 | doesn't build: no `MaxMapSize` for `GOARCH=wasm`, then `bolt_unix.go` needs `syscall.Flock` and `unix.Mmap` | runs; 300 seeds pass, `NoSync` is caught, and it found a [missing directory fsync](findings/bbolt-create-dir-fsync.md) | [`examples/bboltkv`](../examples/bboltkv) |
| `github.com/cockroachdb/pebble/v2` v2.1 | doesn't build: `vfs` has no `wasip1` file system (its generic lock file refers to an undefined `defFS`), `rawalloc` has no `maxArrayLen` for `wasm`, and prometheus/procfs v0.10 calls `syscall.Statfs` | runs; needs `strict_dirs` (see below) | [`examples/pebblekv`](../examples/pebblekv) |
| `github.com/dgraph-io/badger/v4` v4.9 | builds, but `Open` fails: ristretto's `mmap` returns `ENOSYS` on `wasip1` | runs, with small memtable and value log sizes | [`examples/badgerkv`](../examples/badgerkv) |
| `github.com/blevesearch/mmap-go` v1.0.4 | doesn't build: its only mapping code is `//go:build` unix | runs through the generic shim; a writable mapping, `msync` and `mmap_early_writeback` work | [`examples/mmapwal`](../examples/mmapwal) |
| `github.com/edsrzf/mmap-go` v1.2.0 | builds, then `mmap` returns `ENOTSUP` from its `wasm` file | runs through the generic shim, which replaces the `wasm` file | swap the import in [`examples/mmapwal`](../examples/mmapwal) |
| `github.com/gofrs/flock` v0.13 | builds, then returns `ErrUnsupported` | runs through the generic shim, which replaces `flock_others.go` | [`examples/mmapwal`](../examples/mmapwal) |
| `golang.org/x/exp/mmap` | runs without a shim, reading the whole file | runs through the generic shim, with a real shared mapping | |
| `github.com/hashicorp/memberlist` v0.7 | doesn't build: `go-sockaddr` has no route lookup for `wasip1` | runs, with `net_overlay`; the shim answers as `go-sockaddr` does on other platforms without a route table, and a node binds and advertises its own IP | [`examples/memberlist`](../examples/memberlist) |

## What the shims do, and why crash results stay real

A shim only replaces calls that WASI can't make. Every write still goes to a file, and every durability point is still a real fsync on seedfall's simulated disk, so crashes, torn writes and fsync errors behave as they do for any other code.

- **Read-only `mmap`** (bbolt) becomes a heap copy of the file, re-read on every remap. bbolt writes pages with `pwrite` and syncs with `fdatasync`, never through the mapping. A `MAP_SHARED` mapping sees those writes immediately, so the shim copies each successful `pwrite` into the heap image too. The file stays the only source of truth after a crash.
- **Shared `mmap`** (badger through ristretto's `z` package, the generic shim) becomes a heap buffer that seedfall's disk knows about. A `pwrite` to the file updates every shared mapping of it, and pages written through a writable mapping reach the page cache at `msync`, at `munmap`, when the file is read with `pread` or mapped again, when it is fsynced, and when the process exits cleanly. `fsync` and `fdatasync` make every dirty page of the file durable. `MS_SYNC` flushes only the range it is given, then fsyncs the file; `MS_ASYNC` does not fsync. Truncating a file clears any part of a mapping past the new size. A `MAP_PRIVATE` mapping is a snapshot. Dirty pages that were never `msync`ed are lost on a crash unless [`mmap_early_writeback`](#early-writeback-of-mapped-pages) keeps them.
- **`flock` and file locks** become an in-process lock table. Each node is one process, and a crash starts a fresh process, so this matches what the lock guarantees.
- **`madvise`, `mlock` and disk-usage queries** are no-ops or report "unsupported". They are hints that don't change what is on disk.
- **Route lookups** (`go-sockaddr`) report that no route is known, as the library does on `js` and `plan9`. Only the automatic choice of a private IP depends on them.

## The generic mmap and flock shim

Most libraries put their mapping code in a file constrained to unix, and it calls `unix.Mmap`, `syscall.Mmap`, `unix.Flock` or `syscall.Flock`. You don't need a shim for each one. With `compat_overlay`, seedfall adds `Mmap`, `Munmap`, `Msync`, `Mlock`, `Munlock`, `Madvise` and `Flock` to `syscall` on `wasip1`, and a matching `golang.org/x/sys/unix`. It then finds files in your dependencies that are excluded on `wasip1`, would build on Linux, and call one of those four functions, and compiles them for `wasip1`. If such a file conflicts with a stub the library already builds on `wasip1`, like `gofrs/flock`'s `flock_others.go`, the stub is removed, but only when the file declares everything the stub declares. It does not touch your own module, or a file whose name ends in `_linux`, which normally assumes much more of Linux.

A file that uses anything else missing on `wasip1`, such as `unix.Fallocate`, fails to compile and the build error names the symbol. Run `seedfall init -n` to see which dependencies are covered: every `wasip1_blockers` entry has a `shim` line naming the shim or saying `none: needs a shim`, and `compat_shims` lists every dependency `compat_overlay` will touch, including ones like `gofrs/flock` that compile but fail at run time. `init` turns on `compat_overlay` when it finds any. `flock` is held by a table inside the node's process, keyed by inode: a lock is released by `LOCK_UN`, by closing the file descriptor that holds it, or when the process dies. A blocking `Flock` retries on a virtual 1 ms sleep; because `syscall` cannot import `time`, the overlay reaches `time.Sleep` through one `go:linkname`, the only one in seedfall.

## Early writeback of mapped pages

A kernel can write back any dirty page of a shared mapping at any time, so a program must not assume unsynced mapped data stayed volatile. With `"mmap_early_writeback": 0.5` in `faults`, each dirty page a crash finds in a writable shared mapping is treated as already written back with that probability, so it survives the crash where it would otherwise be lost. Each page is recorded as a `mmap_early_writeback` fault with the node and a `mapped page #N` count, so it replays, shrinks and edits like any other runtime disk fault. [`examples/mmapwal`](../examples/mmapwal) shows a checkpoint-and-checksum design that assumes unsynced pages can't reach the disk: it passes without the fault and fails with it.

Written-back pages are the page's content at the moment of the crash. A page written back earlier and dirtied again would have older content on a real kernel; that outcome is not generated.

## How it works

The shims are Go files embedded in seedfall, one directory per import path under [`engine/compat`](../engine/compat). At build time seedfall runs `go list -deps` for your nodes. For each dependency package with a shim, it copies the dependency's module into `.seedfall/compat`, adds the shim files, and removes every file the shim replaces. It builds with `-modfile` pointing at a copy of your `go.mod` that has a `replace` for each copied module. Your `go.mod`, `go.sum` and module cache are not touched. (`go build -overlay` can't be used here: Go refuses overlays for files in the module cache.)

Nothing is patched by text. A file is replaced when it declares something the shim declares, and then every top-level function, method, type, variable and constant it declares must be in the shim. If a new release adds a declaration to a replaced file, the build stops with an error that names it:

```
compat overlay: go.etcd.io/bbolt/bolt_unix.go declares fdatasync2, which the wasip1 shim does not provide; this version is not supported yet
```

Adding support for another library is adding a directory of `.go.txt` files. The generic shim adds `seedfall_mmap_wasip1.go` to `syscall` with the same `-overlay` mechanism as `net_overlay`, so the first build after a Go upgrade recompiles the standard library's `syscall` once.

## Pebble and directory fsyncs

Pebble records which manifest is current with an empty marker file. It creates the file and fsyncs the directory, but never fsyncs the empty file itself. On a real filesystem the directory fsync makes it durable. seedfall's default disk model drops every file that was never fsynced on a crash, so pebble would fail on its first restart with `database already exists and is not pristine`. Run pebble with `"strict_dirs": true`, where directory fsyncs decide which entries survive, as on a real filesystem.

## Not covered

- A mapping whose bytes are only read by another process. Each node is one process, so a mapping is only ever coherent with the node's own `pwrite` calls.
- Two mappings of one file see each other's writes only after one of the flushes above, because there is no page fault to hook.
- Mapping a file after its last name is unlinked: `Mmap` returns `EBADF`.
- `seedfall inspect` shows mapped writes only once they have reached the page cache, for the same reason.
- `mremap`, and code that needs more of `syscall` or `x/sys/unix` than the functions above. The build error names what is missing.
- Libraries seedfall doesn't have a verified shim for: `github.com/boltdb/bolt`, `github.com/hashicorp/raft-boltdb` before its `v2` (which uses bbolt), `github.com/dgraph-io/ristretto` before `v2`, and `github.com/cockroachdb/pebble` before `v2` (a `v2`-style shim made it build, but acknowledged writes were lost on restart, so it isn't shipped).
- Libraries that need cgo, such as RocksDB bindings or `mattn/go-sqlite3`.
