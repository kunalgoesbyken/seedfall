# Networking

Nodes in seedfall talk over a simulated network, so seedfall can delay, drop and cut messages between them. This page explains the two ways to connect your code to it: call seedfall's `sim.Listen` and `sim.Dial`, or leave your code alone and turn on `net_overlay`. Read it when nodes cannot reach each other or you are wiring up a new project.

seedfall gives every node a simulated network: ordered TCP byte streams and unordered UDP datagrams with seeded latency, per-link latency overrides, partitions that hold TCP traffic until they heal and drop UDP datagrams, one-way clogs that stop, jam or slow a link, slow nodes, and connection resets when a peer crashes or a reset fault fires. Node names are host names, so a node named `server` is reachable at `server:7000`.

How each network fault behaves and how to configure it is in [faults.md](faults.md#network-faults).

There are two ways to connect your code to the simulated network.

## Option 1: `sim.Listen` and `sim.Dial`

```go
import "github.com/kunalgoesbyken/seedfall/sim"

ln, err := sim.Listen("tcp", ":7000")
conn, err := sim.Dial("tcp", "server:7000")
pc, err := sim.ListenPacket("udp", ":7100")
```

`sim.Listen` and `sim.Dial` take `tcp`, `tcp4` or `tcp6`, and `sim.ListenPacket` takes `udp`, `udp4` or `udp6`; any other network, such as `unix`, is an error in the simulator and outside it, so a node does not behave differently in production. They return a standard `net.Listener`, `net.Conn` and `net.PacketConn`. Outside the simulator they call `net.Listen`, `net.Dial` and `net.ListenPacket`, so production builds are unaffected. This works on every Go version seedfall supports, and it is how most of the examples are written. A connection also has `CloseRead` and `CloseWrite`, reached with a type assertion to an interface that declares them.

If a library takes a custom dialer or listener, pass these in. hashicorp/raft's `NewNetworkTransport` accepts a `StreamLayer` built on them, and NATS clients accept `nats.SetCustomDialer`.

## Option 2: zero code changes with `net_overlay`

```json
{ "net_overlay": true }
```

With `net_overlay`, unmodified programs that call `net.Listen`, `net.Dial`, `net.ListenPacket`, `net.ListenUDP` and `net.DialUDP` use the simulated network, and so do `net/http` servers and clients, and libraries such as hashicorp/raft's stock `NewTCPTransport` and hashicorp/memberlist. `examples/httpkv` is a plain `net/http` server and client with no seedfall imports in the server, and `examples/memberlist` is an unmodified memberlist cluster gossiping over simulated UDP.

How it works: on `wasip1`, Go's `net` package uses an in-process fake network. At build time, seedfall reads that package from your own `GOROOT` and builds an overlay with `go build -overlay`. It makes four exact-text patches: `socket()` is renamed so a seedfall version can wrap it, `accept()` gets a hook for seedfall listeners, and `setReadBuffer` and `setWriteBuffer` accept any size for simulated sockets, because the simulated queues have no size. Then it adds one file to package `net` that sends TCP and UDP sockets to the simulator. Node names resolve through a simulated `/etc/hosts`, and `localhost` means the node itself.

If a future Go release changes those internals, the build fails with a clear "this Go version is not supported yet" error instead of misbehaving. Verified on Go 1.27.

## Addresses, port 0 and half-close

Both options behave the same way:

- Listening or binding on port 0 picks a free port from `49152`..`65535`, in a sequence kept per node, and `Addr()` or `LocalAddr()` returns the real port.
- Every dial takes its own port from the same range, so an accepted connection's `RemoteAddr()` is the dialer's real address and matches the dialer's `LocalAddr()`. Under `net_overlay` an address is the node's IP from the simulated `/etc/hosts`, such as `10.77.0.2:49152`. Through `sim` it is the node name, such as `client:49152`.
- `CloseWrite` sends EOF to the peer after the data already written, and the other direction keeps working. Writing after `CloseWrite` fails with `EPIPE`. `CloseRead` makes every later read return EOF.

## UDP

A UDP socket on one node talks to any other through the same link table as TCP, and every datagram draws its own latency, so datagrams overtake each other whenever the latency range allows it.

| Condition | What happens to a datagram |
|---|---|
| Partition, or clog `stop` or `jam`, in the sending direction | Dropped. Nothing holds it for the heal |
| Clog `slow` | Arrives `clog_delay` later |
| Slow node | Latency is stretched, as for TCP |
| No socket bound to the address when it is sent, or one connected to a different address | Dropped. A connected sender gets the equivalent of ICMP port unreachable: unless a partition or clog cuts the path back, its next read or send after a round trip fails with `ECONNREFUSED`, once. An unconnected sender hears nothing |
| `udp_loss` | Dropped, recorded as a `udp_drop` fault |
| `udp_dup` | Delivered twice with independent latencies, recorded as a `udp_dup` fault |

Datagram boundaries are kept: a read returns one datagram and discards whatever does not fit in the buffer. A socket's queue has no limit, so `SetReadBuffer` and `SetWriteBuffer` succeed and change nothing, and a sender is never blocked. A crash closes the node's sockets and drops what was queued for them, and `net_reset` faults do not touch UDP, because a datagram socket has no connection to reset. Multicast is not simulated, and `WriteMsgUDP` with out-of-band data and raw reads with flags return `ENOTSUP`.

## Seeing the traffic

`-net-trace` on `run`, `replay`, `logs` or `debug` adds every dial, accept, message sent and message read to the trace, with its connection, size, send and arrival time, and whether a partition or clog held it. UDP sockets show up as `binds udp`, each datagram as `udp <socket>` lines, and a lost one says why. Every failure's `timeline.html` draws the same messages as arrows between nodes. See [timelines and logs](timeline.md).

## Without either

Go's unmodified `net` package on `wasip1` is an in-process fake. A `net.Listen` succeeds but can never be reached from another node. If nodes cannot reach each other, enable `net_overlay` or switch to `sim`.
