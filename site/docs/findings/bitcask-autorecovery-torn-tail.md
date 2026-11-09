# AutoRecovery fails on ordinary torn tails, so Open errors out after a crash mid-Put

**Repo:** prologic/bitcask on git.mills.io, module `go.mills.io/bitcask/v2` (tested on v2.2.0, which is `main` at a5ea7328ad)
**Severity:** after a crash in the middle of an unacknowledged `Put`, `Open` with `WithAutoRecovery(true)` (the default) returns an error instead of truncating the torn record. In one of the two cases, every later `Open` fails too.

`CheckAndRecover` (internal/data/recover.go:19) is supposed to make a crash mid-write harmless: it copies the valid prefix of the last datafile into `<file>.recovered`, renames it over the datafile and deletes the index. Two bugs make it fail on exactly the input it exists for.

## 1. Deleting a missing index fails the whole Open

After a successful repair, `CheckAndRecover` calls `os.Remove(filepath.Join(path, "index"))` and returns any error (recover.go:33). The index is written only by `Close` (bitcask.go:99), and the recovery itself deletes it. So the index is missing whenever the database hasn't been closed cleanly since it was created, or since the last recovery. In that state `Open` fails with:

```
recovering database: error deleting the index on recovery: remove data/db/index: no such file or directory
```

The datafile has already been replaced at that point, so the next `Open` succeeds. Applications that treat an `Open` error as fatal still go down, and a supervisor has to retry.

Reproduction (stock v2.2.0; the server fsyncs `config.json` after `Open` to work around the separate `config.json` issue):

```
cd examples/bitcask
seedfall replay -c seedfall.recovery.json -seed 1
```

```
faults before the failure (1):
  398.735ms  crash server during disk op #56, restart at 762.855ms
```

**Fix:** ignore `os.IsNotExist` when removing the index.

## 2. A record torn inside its 12-byte length prefix is treated as fatal

`Decoder.Decode` reads the 4-byte key size and 8-byte value size with `io.ReadFull` and returns its error unchanged (internal/codec/decoder.go:45-47). If the crash cut the record inside those 12 bytes, that error is `io.ErrUnexpectedEOF`. `IsCorruptedData` (decoder.go:104) only recognizes `errTruncatedData`, `errInvalidKeyOrValueSize` and `errCantDecodeOnNilEntry`, so `recoverDatafile` takes the "unexpected error" path (recover.go:79). `Open` fails with:

```
recovering database: error recovering data file: unexpected error while reading datafile: unexpected EOF
```

Nothing gets repaired, so this repeats on every restart. A tear one byte later, past the prefix, is handled correctly as `errTruncatedData` (decoder.go:57).

Reproduction: on v2.2.0 with only the `config.json` fix and fix 1 applied, `seedfall.torn.json` fails on seed 2. Shrunk:

```
faults before the failure (4):
  1.576175s  partition [client] from the rest until 1.941238s
  1.719562s  crash server, restart at 1.745971s
  1.745971s  crash server during disk op #254, restart at 2.093838s
  2.09639s  crash server during disk op #358, restart at 2.430286s
```

**Fix:** map a short prefix to `errTruncatedData`:

```go
_, err := io.ReadFull(d.r, prefixBuf)
if err == io.ErrUnexpectedEOF {
	return 0, errTruncatedData
}
if err != nil {
	return 0, err
}
```

## Harness

[seedfall](https://github.com/kunalgoesbyken/seedfall) runs a small server on bitcask v2.2.0 with `WithSyncWrites(true)`, `WithAutoRecovery(true)` and a 4 KB `MaxDatafileSize`. It acknowledges `SET` only after `Put` returns. A client writes 300 keys and then checks that every acknowledged key is still there. The simulated disk keeps only fsynced data across a crash, and with torn writes a crash can keep a partial, unsynced last write.

## Fix verified

With both fixes, the `config.json` fix and the `.recovered` fsync fix (filed separately), `seedfall.torn.json` and `seedfall.recovery.json` pass 300 seeds each.
