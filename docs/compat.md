# Storage engines that need mmap or flock

Some storage libraries don't build or run for `GOOS=wasip1` because they call `mmap`, `flock` or other syscalls that WASI doesn't have. With `compat_overlay`, seedfall swaps their platform files for small `wasip1` versions at build time, so you test the library you ship, unmodified:

```json
{ "compat_overlay": true }
```

| Library | Without `compat_overlay` | With it | Example |
|---|---|---|---|
| `go.etcd.io/bbolt` v1.5 | doesn't build: no `MaxMapSize` for `GOARCH=wasm`, then `bolt_unix.go` needs `syscall.Flock` and `unix.Mmap` | runs; 300 seeds pass, `NoSync` is caught, and it found a [missing directory fsync](findings/bbolt-create-dir-fsync.md) | [`examples/bboltkv`](../examples/bboltkv) |
| `github.com/cockroachdb/pebble/v2` v2.1 | doesn't build: `vfs` has no `wasip1` file system (its generic lock file refers to an undefined `defFS`), `rawalloc` has no `maxArrayLen` for `wasm`, and prometheus/procfs v0.10 calls `syscall.Statfs` | runs; needs `strict_dirs` (see below) | [`examples/pebblekv`](../examples/pebblekv) |
| `github.com/dgraph-io/badger/v4` v4.9 | builds, but `Open` fails: ristretto's `mmap` returns `ENOSYS` on `wasip1` | runs, with small memtable and value log sizes | [`examples/badgerkv`](../examples/badgerkv) |
| `github.com/hashicorp/memberlist` v0.7 | doesn't build: `go-sockaddr` has no route lookup for `wasip1` | runs, with `net_overlay`; the shim answers as `go-sockaddr` does on other platforms without a route table, and a node binds and advertises its own IP | [`examples/memberlist`](../examples/memberlist) |

## What the shims do, and why crash results stay real

A shim only replaces calls that WASI can't make. Every write still goes to a file, and every durability point is still a real fsync on seedfall's simulated disk, so crashes, torn writes and fsync errors behave as they do for any other code.

- **Read-only `mmap`** (bbolt) becomes a heap copy of the file, re-read on every remap. bbolt writes pages with `pwrite` and syncs with `fdatasync`, never through the mapping. A `MAP_SHARED` mapping sees those writes immediately, so the shim copies each successful `pwrite` into the heap image too. The file stays the only source of truth after a crash.
- **Writable `mmap`** (badger, through ristretto's `z` package) becomes a heap copy plus a clean copy. `msync` writes back the pages that differ and then fsyncs the file. `munmap` writes them back without an fsync, like dirty pages left in the page cache. Changes made through the mapping and never `msync`ed are lost on a crash. Real kernels may also write some of those pages back early; that outcome isn't generated.
- **`flock` and file locks** become an in-process lock table. Each node is one process, and a crash starts a fresh process, so this matches what the lock guarantees.
- **`madvise`, `mlock` and disk-usage queries** are no-ops or report "unsupported". They are hints that don't change what is on disk.
- **Route lookups** (`go-sockaddr`) report that no route is known, as the library does on `js` and `plan9`. Only the automatic choice of a private IP depends on them.

## How it works

The shims are Go files embedded in seedfall, one directory per import path under [`engine/compat`](../engine/compat). At build time seedfall runs `go list -deps` for your nodes. For each dependency package with a shim, it copies the dependency's module into `.seedfall/compat`, adds the shim files, and removes every file the shim replaces. It builds with `-modfile` pointing at a copy of your `go.mod` that has a `replace` for each copied module. Your `go.mod`, `go.sum` and module cache are not touched. (`go build -overlay` can't be used here: Go refuses overlays for files in the module cache.)

Nothing is patched by text. A file is replaced when it declares something the shim declares, and then every top-level function, method, type, variable and constant it declares must be in the shim. If a new release adds a declaration to a replaced file, the build stops with an error that names it:

```
compat overlay: go.etcd.io/bbolt/bolt_unix.go declares fdatasync2, which the wasip1 shim does not provide; this version is not supported yet
```

Adding support for another library is adding a directory of `.go.txt` files.

## Pebble and directory fsyncs

Pebble records which manifest is current with an empty marker file. It creates the file and fsyncs the directory, but never fsyncs the empty file itself. On a real filesystem the directory fsync makes it durable. seedfall's default disk model drops every file that was never fsynced on a crash, so pebble would fail on its first restart with `database already exists and is not pristine`. Run pebble with `"strict_dirs": true`, where directory fsyncs decide which entries survive, as on a real filesystem.

## Not covered

- Writable `mmap` for libraries other than ristretto. Each library's `mmap` wrapper needs its own shim.
- Libraries that need cgo, such as RocksDB bindings or `mattn/go-sqlite3`.
