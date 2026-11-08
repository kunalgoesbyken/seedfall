# gostman

A [Gostman](https://github.com/kunalgoesbyken/gostman) collection used as the workload. The server is [`examples/httpkv`](../httpkv)'s plain `net/http` key-value server, built straight from that module (`tool httpkv/server` in `go.mod`), which also answers `PUT`, `GET` and `DELETE /kv/{key}` and `GET /health`. The client loads the YAML collection in [`collection`](collection) with Gostman's `runner` package and sends it again and again while seedfall crashes the server, tears its writes and partitions the network.

The collection is the API contract:

| Folder | Request | Checks |
|---|---|---|
| `Write` | `Put value`: `PUT /kv/{{key}}` with body `{{value}}` | status is 200 |
| `Verify` | `Health`: `GET /health` | none, any answer means the server is up |
| | `Get value`: `GET /kv/{{key}}` | status is 200, body is `{{value}}` |
| | `Get deleted key`: `GET /kv/{{deleted_key}}` | status is 404 |
| | `Delete value`: `DELETE /kv/{{key}}` | status is 200 |

Each round writes `k<n>=v<n>` while faults run, retrying until a 200 acknowledges it, then gives the faults another second. Then `sim.QuietFor` stops the faults and heals partitions, and the `Verify` folder runs: the write must still be there, the key deleted in the previous round must still be gone, and this round's key is deleted. A request that gets no answer (the server is restarting) is sent again; a check that fails on an answer fails the seed with the request and the check:

```
FAIL seed 1 at t=13.104695555s (108 scheduler steps, 29.398s real)
  client#1: Gostman checks pass once the faults stop: GET http://server:8080/kv/k6 (Verify/Get value)
  res.status equals 200: The value was 404
  res.body equals v6: The value was "404 page not found\n"
```

| Config | Result |
|---|---|
| `seedfall.json` | fsync before every acknowledgement: passes (120 seeds tested) |
| `-profile nofsync` | `-fsync=false`: fails on seed 1, an acknowledged write is gone after a crash |

```sh
seedfall run -seeds 1-120
seedfall replay -c seedfall.json -profile nofsync -seed 1
```

The client embeds the collection with `go:embed`, because nodes only see their simulated disk. Check the same collection against a server on your machine with the Gostman command line:

```sh
go tool httpkv/server &       # keeps its log in ./data
gostman run collection --env Local --folder Write
gostman run collection --env Local --folder Verify
```

Client flags: `-env` (default `Simulation`), `-write` and `-verify` (folder names), `-rounds`, `-settle`, `-quiet`, `-timeout` and `-retry`. See [docs/gostman.md](../../docs/gostman.md) to use your own collection.
