# httpkv

A key-value server written with plain `net/http` and `http.ListenAndServe`, with no seedfall imports. The client uses plain `http.Get` and `http.Post` against `http://server:8080`. Both run on the simulated network through `"net_overlay": true` ([networking](../../docs/networking.md)).

The server also answers `PUT`, `GET` and `DELETE /kv/{key}` and `GET /health`, which [`examples/gostman`](../gostman) checks with a Gostman collection.

| Config | Result |
|---|---|
| `seedfall.json` | fsync before every response: passes (40 seeds tested) |
| `-profile nofsync` | `-fsync=false`: fails on seed 1, an acknowledged write lost |
