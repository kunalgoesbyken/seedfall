# Open rewrites config.json in place without fsync, so a crash can leave the database permanently unopenable

**Repo:** prologic/bitcask on git.mills.io, module `go.mills.io/bitcask/v2` (tested on v2.2.0, which is `main` at a5ea7328ad; v1.0.2 has the same code)
**Severity:** one crash at the wrong moment and every later `Open` fails with `ErrBadConfig` until someone repairs or deletes `config.json` by hand. No data is lost, but the database stays down.

## Summary

Every read-write `Open` (bitcask.go:620) calls `cfg.Save(configPath)`, which is `os.WriteFile(path, data, mode)` (internal/config/config.go:50). That opens the file with `O_TRUNC`, writes the JSON and closes it, with no fsync, no temporary file and no rename. Until the kernel writes the page back, the on-disk `config.json` can be empty or hold a prefix of the new JSON.

On the next `Open`, `config.Load` fails with `unexpected end of JSON input` and `Open` returns `&ErrBadConfig{err}` (bitcask.go:566-568). The file is never rewritten, because `Save` runs only after a successful load, so every later restart fails the same way.

The exposure window starts at every `Open` and lasts until writeback, typically up to 30 seconds. That makes it most likely in a crash loop, which is exactly when an application keeps reopening the database. ext4's default `auto_da_alloc` forces writeback on truncate-then-rewrite and hides this on many Linux systems. It doesn't help ext4 mounted with `noauto_da_alloc`, XFS or other filesystems, and POSIX doesn't guarantee it.

#96 discussed what `Open` should do when `config.json` can't be read; today it returns `ErrBadConfig`, which turns this crash window into a hard failure.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall). It runs a small server on bitcask v2.2.0 with `WithSyncWrites(true)`, `WithAutoRecovery(true)` and a 4 KB `MaxDatafileSize`. It acknowledges `SET` only after `Put` returns. A client writes 300 keys and then checks that every acknowledged key is still there. The simulated disk keeps only fsynced data across a crash. With torn writes enabled, a crash can keep a random prefix of the unsynced writes, including a partial last write.

```
cd examples/bitcask
seedfall replay -c seedfall.torn.json -seed 1
```

`seedfall shrink` reduces the schedule to one plain crash:

```
faults before the failure (1):
   1.7052s  crash server, restart at 1.942589s
```

On restart: `open data/db: error reading config.json: unexpected end of JSON input`, and the server exits.

| Config | Stock v2.2.0 | Patched |
|---|---|---|
| `SyncWrites`, crashes + partitions (`seedfall.json`) | 300/300 pass | — |
| plus torn writes + crash during I/O (`seedfall.torn.json`) | fails, seed 1 | the `config.json` error never appears; 300/300 pass with the three recovery fixes in the related drafts |
| `SyncWrites: false` (`seedfall.nosync.json`) | fails, seed 1 (expected) | — |

## Suggested fix

Write `config.json` atomically and durably: write a temporary file, fsync it, rename it over `config.json`, then fsync the directory.

```go
func writeFileDurably(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
```

Skipping the rewrite when the serialized config hasn't changed would also remove the window on normal restarts. `meta.json` is saved through the same `os.WriteFile` pattern (`internal.SaveJSONToFile`, internal/utils.go:102) and needs the same treatment.

## Fix verified

With `Config.Save` replaced by the function above, the `config.json` failure disappears from `seedfall.torn.json`. The sweep then hits the two auto-recovery bugs filed separately. With all fixes applied, it passes 300 seeds.
