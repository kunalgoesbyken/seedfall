# Use your Gostman collections as workloads

A [Gostman](https://github.com/kunalgoesbyken/gostman) collection already says what your API promises: each request carries checks such as `res.status equals 200` or `res.body equals {{value}}`. Gostman's Go `runner` package builds for `wasip1`, so a seedfall workload can load the collection and send it while seedfall crashes your service, tears its writes and partitions its network. A check that fails after the faults stop is a broken promise, reported with the request and the check, and replayable from its seed.

[`examples/gostman`](../examples/gostman) is a complete world: `examples/httpkv`'s server and a client that runs the collection in [`examples/gostman/collection`](../examples/gostman/collection).

## The pattern

Split the collection into two folders:

- **Write**: requests that change state. They are sent while faults run and retried until their checks pass, so a pass means the server acknowledged the change.
- **Verify**: requests that check state. They are sent after `sim.QuietFor`, when faults are paused and partitions healed. A request that gets no answer, because the server is still restarting, is sent again. A check that fails on an answer fails the seed.

The client copies the embedded collection onto its simulated disk, loads it and sends one request at a time through a single session:

```go
//go:embed collection
var Collection embed.FS

os.CopyFS(".", Collection)
collection, _ := runner.Load("collection")
session, _ := runner.NewSession(engine.Policy{})
session.Enter(collection, "", "Simulation", nil)
listed, _ := session.Catalog(collection, "Verify")
result := session.Send(ctx, listed[0].Request, listed[0].Folder, vars).Result
sim.Always(!result.Failed, "Gostman checks pass once the faults stop", details)
```

Nodes cannot read files on the host, which is why the collection is embedded. Variables passed to `Send` (`key`, `value`, `deleted_key` in the example) win over the environment, so each round uses fresh keys. The `Simulation` environment points `base_url` at `http://server:8080`, the node name; a `Local` environment points at `127.0.0.1` so the same files run with the Gostman command line.

## Run it

```sh
cd examples/gostman
seedfall run -seeds 1-120
seedfall replay -c seedfall.nofsync.json -seed 1
```

With fsync before every acknowledgement every seed passes. With `-fsync=false` the first seed fails once a crash drops an acknowledged write:

```
FAIL seed 1 at t=13.104695555s (108 scheduler steps, 29.398s real)
  client#1: Gostman checks pass once the faults stop: GET http://server:8080/kv/k6 (Verify/Get value)
  res.status equals 200: The value was 404
  res.body equals v6: The value was "404 page not found\n"
```

The client embeds the Gostman runner and its JavaScript sandbox, so each seed takes about a minute of real time; sweeps get their speed from running seeds in parallel.

## Use your own collection

1. Copy `examples/gostman/client` and `collection.go` next to your service's seedfall config, and put your Gostman workspace folder in `collection/`.
2. Add a `Simulation` environment whose base URL uses the node name from `seedfall.json`.
3. Put the requests that write in one folder and the requests that check in another, and pass their names with `-write` and `-verify`.
4. Enable `"net_overlay": true` so the runner's plain `net/http` client reaches the simulated network.

The example uses HTTP requests with assertions. Anything that needs the host, such as `{{$file(...)}}`, local commands or an OAuth sign-in in a browser, cannot work inside a node.
