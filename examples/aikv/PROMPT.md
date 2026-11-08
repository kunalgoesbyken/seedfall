# Prompt

Every sample got this prompt, word for word, in a fresh session with no other context. Only the directory changed.

```text
Write a Go package for an embedded, persistent key-value store. Work only inside `gen/<model>-<n>` and do not read or modify anything outside it. Create a Go module there named `kvstore`, containing a package `kv` at the module root.

API:
- `func Open(dir string) (*DB, error)`
- `func (db *DB) Put(key string, value []byte) error`
- `func (db *DB) Get(key string) ([]byte, bool, error)`
- `func (db *DB) Delete(key string) error`
- `func (db *DB) Close() error`

Requirements:
- Durability: once Put or Delete returns nil, the change must survive a process crash or power loss at any moment afterwards. That includes crashes during a later Open/recovery or during compaction.
- Recovery must never return a value that was never written, and must never bring back a deleted key or an overwritten value.
- It must cope with partially written data left by a crash.
- The on-disk data must not grow without bound under overwrites and deletes, so implement compaction. Compaction must be crash-safe too.
- It must be safe for concurrent use by multiple goroutines in one process.
- Go standard library only, no cgo. It must build with `GOOS=wasip1 GOARCH=wasm go build ./...` as well as for normal targets.
- Write tests and make sure `go test ./...` passes.

Treat this as production code. When you are done, reply with a short description of your durability design: the file layout, when you fsync, and how recovery and compaction work.
```
