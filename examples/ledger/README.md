# ledger

A deposit ledger written with plain `net` and the [Antithesis Go SDK](https://github.com/antithesishq/antithesis-sdk-go), and no seedfall imports. The server appends each deposit to `data/ledger.log`, fsyncs it and acknowledges it. On recovery it drops a torn final entry. The client calls `lifecycle.SetupComplete` once the server answers, makes 150 deposits with amounts from `random.GetRandom`, and checks with `assert.Always` that the total accounts for every acknowledged deposit.

| Config | Result |
|---|---|
| `seedfall.json` | passes (100 seeds tested) under crashes, torn writes and crashes mid-I/O |
| `seedfall.nofsync.json` | `-fsync=false`: fails on seed 1, acknowledged deposits lost |

See [docs/antithesis.md](../../docs/antithesis.md).
