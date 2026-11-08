# ledger

A deposit ledger written with plain `net` and the [Antithesis Go SDK](https://github.com/antithesishq/antithesis-sdk-go), and no seedfall imports. The server appends each deposit to `data/ledger.log`, fsyncs it and acknowledges it. On recovery it drops a torn final entry. The client calls `lifecycle.SetupComplete` once the server answers, makes 150 deposits with amounts from `random.GetRandom`, and checks with `assert.AlwaysGreaterThanOrEqualTo` and `assert.AlwaysLessThanOrEqualTo` that the total covers every acknowledged deposit and nothing that was never sent. `assert.SometimesAll` confirms that some seed kept a deposit whose acknowledgement a crash lost, and `lifecycle.SendEvent` records the final totals in the trace.

| Config | Result |
|---|---|
| `seedfall.json` | passes (100 seeds tested) under crashes, torn writes and crashes mid-I/O |
| `seedfall.nofsync.json` | `-fsync=false`: fails on seed 1, acknowledged deposits lost (`{"left":4203,"right":5608,...}`) |

See [docs/antithesis.md](../../docs/antithesis.md).
