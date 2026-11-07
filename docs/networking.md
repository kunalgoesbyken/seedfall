# Networking

seedfall gives every node a simulated network: ordered byte streams with seeded latency, partitions that hold traffic until they heal, and connection resets when a peer crashes. Node names are host names, so a node named `server` is reachable at `server:7000`.

There are two ways to connect your code to it.

## Option 1: `sim.Listen` and `sim.Dial`

```go
import "github.com/kunalgoesbyken/seedfall/sim"

ln, err := sim.Listen("tcp", ":7000")
conn, err := sim.Dial("tcp", "server:7000")
```

They return a standard `net.Listener` and `net.Conn`. Outside the simulator they call `net.Listen` and `net.Dial`, so production builds are unaffected. This works on every Go version seedfall supports, and it is how most of the examples are written.

If a library takes a custom dialer or listener, pass these in. hashicorp/raft's `NewNetworkTransport` accepts a `StreamLayer` built on them, and NATS clients accept `nats.SetCustomDialer`.

## Option 2: zero code changes with `net_overlay`

```json
{ "net_overlay": true }
```

With `net_overlay`, unmodified programs that call `net.Listen` and `net.Dial` use the simulated network, and so do `net/http` servers and clients, and libraries such as hashicorp/raft's stock `NewTCPTransport`. `examples/httpkv` is a plain `net/http` server and client with no seedfall imports in the server.

How it works: on `wasip1`, Go's `net` package uses an in-process fake network. At build time, seedfall reads that package from your own `GOROOT` and builds an overlay with `go build -overlay`. It makes two exact-text patches: `socket()` is renamed so a seedfall version can wrap it, and `accept()` gets a hook for seedfall listeners. Then it adds one file to package `net` that sends TCP sockets to the simulator. Node names resolve through a simulated `/etc/hosts`, and `localhost` means the node itself.

If a future Go release changes those internals, the build fails with a clear "this Go version is not supported yet" error instead of misbehaving. Verified on Go 1.27.

Current limits:
- TCP only. UDP keeps using Go's in-process fake network, so gossip protocols (memberlist, serf) are not simulated.
- Listening on port 0 is not supported.
- `CloseRead` and `CloseWrite` are not supported.
- The remote address of an accepted connection is a placeholder.

## Without either

Go's unmodified `net` package on `wasip1` is an in-process fake. A `net.Listen` succeeds but can never be reached from another node. If nodes cannot reach each other, enable `net_overlay` or switch to `sim`.
