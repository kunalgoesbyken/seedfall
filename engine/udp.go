package engine

import (
	"fmt"
	"slices"
	"time"
)

const (
	lostNoSocket  = "no socket bound to the address"
	lostConnected = "the socket is connected to another address"
	lostLink      = "held by a partition or clog"
	lostFault     = "dropped by a udp_drop fault"
)

type datagram struct {
	at       int64
	from     string
	fromInst string
	data     []byte
	sent     int64
	msg      int
	sock     int32
}

type udpSocket struct {
	id      int32
	owner   *instance
	key     string
	remote  string
	inbox   []datagram
	refused int64
}

func (n *network) udpTaken(key string) bool {
	_, taken := n.udp[key]
	return taken
}

func (n *network) bindUDP(inst *instance, addr string) int32 {
	key, code := n.bind(inst, addr, n.udpTaken)
	if code < 0 {
		return code
	}
	s := &udpSocket{id: n.alloc(), owner: inst, key: key, refused: never}
	n.udp[key] = s
	n.handles[s.id] = s
	n.w.netLine(inst, "net %s binds udp %s", inst.label, key)
	return s.id
}

func (n *network) socket(inst *instance, id int32) *udpSocket {
	s, ok := n.handles[id].(*udpSocket)
	if !ok || s.owner != inst {
		return nil
	}
	return s
}

func (n *network) connectUDP(inst *instance, id int32, addr string) int32 {
	s := n.socket(inst, id)
	if s == nil {
		return codeBadFD
	}
	s.remote = n.target(inst, addr)
	return 0
}

func (n *network) sendto(inst *instance, id int32, addr string, data []byte) int32 {
	s := n.socket(inst, id)
	if s == nil {
		return codeBadFD
	}
	if n.takeRefused(s) {
		return codeRefused
	}
	to := s.remote
	if addr != "" {
		to = n.target(inst, addr)
	}
	if to == "" {
		return codeNoDest
	}
	n.datagram(s, to, data)
	return int32(len(data))
}

func (n *network) datagram(s *udpSocket, to string, data []byte) {
	src := s.owner.node
	dst, _ := splitHostPort(to)
	src.datagrams++
	op := src.datagrams
	m := Message{Conn: s.id, Kind: MessageDatagram, From: src.name(), To: dst, FromInstance: s.owner.label, Bytes: len(data), Sent: time.Duration(n.w.now), Arrives: time.Duration(n.w.now), Read: unread}
	peer := n.udp[to]
	if peer != nil {
		m.ToInstance = peer.owner.label
	}
	link := n.w.link(src.name(), dst)
	switch {
	case link.held > 0:
		m.Lost = lostLink
	case peer == nil:
		m.Lost = lostNoSocket
	case peer.remote != "" && peer.remote != s.key:
		m.Lost = lostConnected
	case n.w.udpFault(src, FaultUDPDrop, n.w.spec.UDPLoss, op, dst):
		m.Lost = lostFault
	}
	if m.Lost != "" {
		n.w.rec.message(m)
		n.w.netLine(s.owner, "net %s -> %s udp %d: %d bytes, lost, %s", s.owner.label, to, s.id, len(data), m.Lost)
		if m.Lost == lostNoSocket || m.Lost == lostConnected {
			n.portUnreachable(s, dst, link)
		}
		return
	}
	copies := 1
	if n.w.udpFault(src, FaultUDPDup, n.w.spec.UDPDup, op, dst) {
		copies = 2
	}
	for i := range copies {
		at := n.w.now + n.w.latency(src.name(), dst) + link.delay
		m.Arrives, m.Duplicate = time.Duration(at), i > 0
		n.enqueue(s, peer, datagram{at: at, from: s.key, fromInst: s.owner.label, data: data, sent: n.w.now, sock: s.id}, m)
	}
}

func (n *network) enqueue(from, to *udpSocket, d datagram, m Message) {
	d.msg = n.w.rec.message(m)
	i := slices.IndexFunc(to.inbox, func(o datagram) bool { return o.at > d.at })
	if i < 0 {
		i = len(to.inbox)
	}
	to.inbox = slices.Insert(to.inbox, i, d)
	to.owner.wakeAt = min(to.owner.wakeAt, d.at)
	n.w.netLine(from.owner, "net %s -> %s udp %d: %d bytes, arrives %s%s", d.fromInst, to.owner.label, d.sock, len(d.data), fmtTime(d.at), deliveryNote(m))
}

func (n *network) portUnreachable(s *udpSocket, dst string, there linkState) {
	src := s.owner.node.name()
	back := n.w.link(dst, src)
	if s.remote == "" || back.held > 0 {
		return
	}
	s.refused = min(s.refused, n.w.now+there.delay+n.w.latency(src, dst)+back.delay+n.w.latency(dst, src))
	s.owner.wakeAt = min(s.owner.wakeAt, s.refused)
	n.w.netLine(s.owner, "net %s udp %d: port unreachable arrives %s", s.owner.label, s.id, fmtTime(s.refused))
}

func (n *network) takeRefused(s *udpSocket) bool {
	if s.refused > n.w.now {
		return false
	}
	s.refused = never
	return true
}

func (n *network) recvfrom(inst *instance, id int32, buf []byte) (int32, string) {
	s := n.socket(inst, id)
	if s == nil {
		return codeBadFD, ""
	}
	if n.takeRefused(s) {
		return codeRefused, ""
	}
	if len(s.inbox) == 0 || s.inbox[0].at > n.w.now {
		return codeAgain, ""
	}
	d := s.inbox[0]
	s.inbox = s.inbox[1:]
	n.w.rec.read(d.msg, n.w.now)
	n.w.netLine(inst, "net %s <- %s udp %d: %d bytes sent at %s", inst.label, d.fromInst, d.sock, len(d.data), fmtTime(d.sent))
	return int32(copy(buf, d.data)), d.from
}

func (n *network) closeUDP(s *udpSocket) {
	delete(n.udp, s.key)
	delete(n.handles, s.id)
}

func (w *world) udpFault(src *node, kind string, rate float64, op int64, dst string) bool {
	if w.shielded[src.name()] || w.shielded[dst] || !w.rated(src, kind, rate, op, w.udpRand) {
		return false
	}
	f := Fault{At: Duration(w.now), Kind: kind, Node: src.name(), To: dst, Op: op}
	w.ioFaults = append(w.ioFaults, f)
	w.trace.about(w.now, nodeLine(f.Node), "fault %s", f.udpText())
	return true
}

func (f Fault) udpText() string {
	verb := "drop"
	if f.Kind == FaultUDPDup {
		verb = "duplicate"
	}
	return fmt.Sprintf("%s %s's udp datagram #%d to %s", verb, f.Node, f.Op, f.To)
}
