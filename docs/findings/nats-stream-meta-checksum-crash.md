# DRAFT ISSUE (not filed): a crash during stream creation leaves meta.inf without meta.sum, and the stream then disappears on every later restart

**Repo:** nats-io/nats-server (tested on v2.15.2, single server, JetStream file storage, `sync_always: true`)
**Severity:** acknowledged messages become invisible after a restart. The stream accepts and acknowledges publishes normally after the first restart, and is silently skipped on every restart after that. No strict-POSIX assumptions needed: this reproduces with ordinary "a rename and an fsynced file are durable" semantics.

## Summary

`fileStore.writeStreamMeta` (`server/filestore.go:1011`) writes the stream config and its checksum as two separate atomic writes: first `meta.inf`, then `meta.sum`. A crash between the two leaves `meta.inf` on disk with no `meta.sum`.

On restart, the account's stream recovery (`server/jetstream.go:1453`) skips the stream:

```
[WRN]   Missing stream checksum file "data/jetstream/$G/streams/EVENTS/meta.sum"
```

The client sees no stream and creates it again. `newFileStoreWithCreated` (`server/filestore.go:636-643`) only rewrites the metadata when `meta.inf` is missing or empty:

```go
meta := filepath.Join(fcfg.StoreDir, JetStreamMetaFile)
fi, err := os.Stat(meta)
if err != nil && os.IsNotExist(err) || fi != nil && fi.Size() == 0 {
    if err := fs.writeStreamMeta(); err != nil {
```

`meta.inf` exists, so `meta.sum` is never written. The re-created stream is fully usable: it stores and acknowledges publishes with `sync_always`. But the store directory still has no checksum, so on the next restart the stream is skipped again, together with every message acknowledged since. The message blocks are still on disk, but the server doesn't load them.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall): an embedded nats-server 2.15.2 with `SyncAlways: true` on a simulated disk, and a client that creates a file-backed R1 stream, publishes 300 messages and reads every acknowledged sequence back.

```
cd examples/natsjs
seedfall replay -c seedfall.always.json -seed 40 -faults metasum.faults.json
```

Two faults are enough:

1. The server crashes during stream creation, after `meta.inf` has been renamed into place and before `meta.sum` is.
2. Later, after the re-created stream has acknowledged 300 publishes, the server crashes once more.

```
0.182s  fault crash node#1 during disk op 9, restart at 0.687s
0.687s  node#2 | [WRN]   Missing stream checksum file ".../EVENTS/meta.sum"
12.116s client#1| 300 of 300 publishes acknowledged, verifying
13.105s fault crash node#2, restart at 13.809s
13.809s node#3 | [WRN]   Missing stream checksum file ".../EVENTS/meta.sum"
14.222s FAIL  client#1: stream EVENTS lost after restart, 214 acknowledged messages gone
```

Only the first crash has to land at a precise moment. In practice that moment is any process kill, OOM kill or power loss while a stream is being created; after that the stream is a time bomb that goes off on the next restart.

## Suggested fix

Treat a missing checksum like a missing config when opening a store, so the re-created stream rewrites both files:

```go
fi, err := os.Stat(meta)
_, sumErr := os.Stat(filepath.Join(fcfg.StoreDir, JetStreamMetaFileSum))
if err != nil && os.IsNotExist(err) || fi != nil && fi.Size() == 0 || os.IsNotExist(sumErr) {
    if err := fs.writeStreamMeta(); err != nil {
```

An alternative is for recovery to recompute and rewrite `meta.sum` when `meta.inf` parses cleanly, which would also bring back streams already in this state.

**Fix verified:** with this change applied to a local copy of v2.15.2, the failing schedule above passes, and so do 150 random seeds of the same configuration (crashes, torn writes, crash during disk I/O).

## Related, strict-POSIX only (lower priority, could be a follow-up)

Under strict POSIX directory semantics, where a new name is durable only after its parent directory is fsynced, two more gaps show up. Both lose the whole stream or a whole message block after one crash:

- New directories are created with `os.MkdirAll` and their parents are never fsynced: the store directory at `filestore.go:439`, `msgs/` and `obs/` at `filestore.go:487` and `:490`, and in `jetstream.go:449` and `:1291`.
- New message block files are created at `filestore.go` in `newMsgBlockForWrite` (`os.OpenFile(mb.mfn, os.O_CREATE|os.O_RDWR, ...)`) without fsyncing `msgs/`, even with `sync_always`. `msgs/` is only fsynced by the periodic `syncBlocks` pass, every `sync_interval`, two minutes by default.

On ext4, XFS and btrfs, fsyncing a new file in practice also makes its directory entry durable, so typical Linux deployments are probably not affected by these two. That is the same caveat as the ALICE study (OSDI 2014). Repro: `seedfall run -c seedfall.strict.json` (stock v2.15.2 fails on seed 1). With durable `MkdirAll` plus a `msgs/` fsync after block creation in `sync_always` mode, 150 strict seeds pass.
