# netcompat

A conformance check of the socket features real servers depend on, run through `net_overlay` with plain `net` calls and through `sim` with `-profile sim`. The client asks the server over TCP for a port the server just bound with port 0, then:

- writes a request and calls `CloseWrite`, reads the answer, and checks the server read to EOF and saw the dialer's real address
- calls `CloseRead` on the control connection and checks a read returns EOF
- checks that writing after `CloseWrite` fails with `EPIPE`
- checks that a connected UDP socket to an unbound port is refused, through `net_overlay`
- binds a UDP socket on port 0, exchanges datagrams with an echo server under 10% loss and duplication and partitions, and checks the reply's source address

| Config | Result |
|---|---|
| `seedfall.json` | `net_overlay`: passes (20 seeds, `-check` clean) |
| `-profile sim` | `sim.Listen`, `sim.Dial` and `sim.ListenPacket`: passes (20 seeds, `-check` clean) |
