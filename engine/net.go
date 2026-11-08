package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	codeAgain   = -1
	codeEOF     = -2
	codeReset   = -3
	codeRefused = -4
	codeInUse   = -5
	codeBadFD   = -6
)

type chunkKind int

const (
	chunkData chunkKind = iota
	chunkEOF
	chunkReset
)

type chunk struct {
	at   int64
	kind chunkKind
	data []byte
	sent int64
	msg  int
	seen bool
}

type endpoint struct {
	id       int32
	conn     int32
	connMsg  int
	owner    *instance
	peer     *endpoint
	inbox    []chunk
	lastSent int64
	acceptAt int64
	gone     bool
}

type listener struct {
	id      int32
	owner   *instance
	key     string
	backlog []*endpoint
}

type network struct {
	w         *world
	nextID    int32
	listeners map[string]*listener
	handles   map[int32]any
	names     map[string]string
	hosts     []byte
}

func newNetwork(w *world) *network {
	n := &network{w: w, listeners: map[string]*listener{}, handles: map[int32]any{}, names: map[string]string{}}
	n.hosts = []byte("127.0.0.1 localhost\n")
	for i, s := range w.cfg.all() {
		ip := fmt.Sprintf("10.77.%d.%d", (i+1)/256, (i+1)%256)
		n.names[ip] = s.Name
		n.hosts = fmt.Appendf(n.hosts, "%s %s\n", ip, s.Name)
	}
	return n
}

func (n *network) resolve(inst *instance, host string) string {
	host = strings.Trim(host, "[]")
	switch host {
	case "", "localhost", "127.0.0.1", "::1", "0.0.0.0", "::":
		return inst.node.name()
	}
	if name, ok := n.names[host]; ok {
		return name
	}
	return host
}

func (n *network) alloc() int32 {
	n.nextID++
	return n.nextID
}

func splitHostPort(addr string) (string, string) {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return addr, ""
	}
	return addr[:i], addr[i+1:]
}

func (n *network) listen(inst *instance, addr string) int32 {
	host, port := splitHostPort(addr)
	if n.resolve(inst, host) != inst.node.name() {
		return codeRefused
	}
	key := inst.node.name() + ":" + port
	if _, taken := n.listeners[key]; taken {
		return codeInUse
	}
	l := &listener{id: n.alloc(), owner: inst, key: key}
	n.listeners[key] = l
	n.handles[l.id] = l
	return l.id
}

func (n *network) dial(inst *instance, addr string) int32 {
	host, port := splitHostPort(addr)
	host = n.resolve(inst, host)
	l, ok := n.listeners[host+":"+port]
	from := inst.node.name()
	if !ok || n.w.cut(from, host) {
		n.w.netLine(inst, "net %s dials %s, refused", inst.label, addr)
		return codeRefused
	}
	there := n.w.link(from, host)
	acceptAt := n.w.now + n.w.latency(from, host)
	if held := max(there.held, n.w.link(host, from).held); held > 0 {
		acceptAt = max(acceptAt, held+n.w.latency(from, host))
	}
	acceptAt += there.delay
	client := &endpoint{id: n.alloc(), owner: inst}
	client.conn = client.id
	server := &endpoint{conn: client.id, owner: l.owner, peer: client, acceptAt: acceptAt}
	client.peer = server
	server.connMsg = n.w.rec.message(Message{Conn: client.id, Kind: MessageConnect, From: from, To: host, FromInstance: inst.label, ToInstance: l.owner.label, Sent: time.Duration(n.w.now), Arrives: time.Duration(acceptAt), Read: unread, Held: there.held > 0})
	n.w.netLine(inst, "net %s dials %s conn %d, arrives %s", inst.label, addr, client.id, fmtTime(acceptAt))
	l.backlog = append(l.backlog, server)
	l.owner.wakeAt = min(l.owner.wakeAt, server.acceptAt)
	n.handles[client.id] = client
	return client.id
}

func (n *network) accept(inst *instance, id int32) int32 {
	l, ok := n.handles[id].(*listener)
	if !ok || l.owner != inst {
		return codeBadFD
	}
	i := slices.IndexFunc(l.backlog, func(e *endpoint) bool { return e.acceptAt <= n.w.now })
	if i < 0 {
		return codeAgain
	}
	ep := l.backlog[i]
	l.backlog = slices.Delete(l.backlog, i, i+1)
	ep.id = n.alloc()
	n.handles[ep.id] = ep
	n.w.rec.read(ep.connMsg, n.w.now)
	n.w.netLine(inst, "net %s accepts conn %d from %s", inst.label, ep.conn, ep.peer.owner.label)
	return ep.id
}

func (n *network) endpoint(inst *instance, id int32) *endpoint {
	ep, ok := n.handles[id].(*endpoint)
	if !ok || ep.owner != inst {
		return nil
	}
	return ep
}

func (n *network) send(from *endpoint, kind chunkKind, data []byte) {
	to := from.peer
	src, dst := from.owner.node.name(), to.owner.node.name()
	link := n.w.link(src, dst)
	at := n.w.now + n.w.latency(src, dst)
	if link.held > 0 {
		at = max(at, link.held+n.w.latency(src, dst))
	}
	at += link.delay
	at = max(at, from.lastSent)
	from.lastSent = at
	m := Message{Conn: from.conn, Kind: chunkNames[kind], From: src, To: dst, FromInstance: from.owner.label, ToInstance: to.owner.label, Bytes: len(data), Sent: time.Duration(n.w.now), Arrives: time.Duration(at), Read: unread, Held: link.held > 0}
	n.deliver(to, chunk{at: at, kind: kind, data: data, sent: n.w.now}, m)
	if n.w.duplicates(to.owner.node, kind) {
		m.Duplicate = true
		n.deliver(to, chunk{at: at, kind: kind, data: data, sent: n.w.now}, m)
	}
	to.owner.wakeAt = min(to.owner.wakeAt, at)
}

func (n *network) deliver(to *endpoint, c chunk, m Message) {
	c.msg = n.w.rec.message(m)
	to.inbox = append(to.inbox, c)
	if n.w.netTrace {
		n.w.netLine(to.peer.owner, "net %s -> %s conn %d: %s, arrives %s%s", m.FromInstance, m.ToInstance, m.Conn, chunkText(c), fmtTime(c.at), deliveryNote(m))
	}
}

func chunkText(c chunk) string {
	if c.kind == chunkData {
		return fmt.Sprintf("%d bytes", len(c.data))
	}
	return chunkNames[c.kind]
}

func deliveryNote(m Message) string {
	switch {
	case m.Duplicate:
		return ", a duplicate"
	case m.Held:
		return ", held by a partition or clog"
	}
	return ""
}

func (n *network) seen(ep *endpoint, c *chunk) {
	if c.seen {
		return
	}
	c.seen = true
	n.w.rec.read(c.msg, n.w.now)
	if n.w.netTrace {
		n.w.netLine(ep.owner, "net %s <- %s conn %d: %s sent at %s", ep.owner.label, ep.peer.owner.label, ep.conn, chunkText(*c), fmtTime(c.sent))
	}
}

func (n *network) write(inst *instance, id int32, data []byte) int32 {
	ep := n.endpoint(inst, id)
	if ep == nil {
		return codeBadFD
	}
	if ep.peer.gone {
		return codeReset
	}
	n.send(ep, chunkData, data)
	return int32(len(data))
}

func (n *network) read(inst *instance, id int32, buf []byte) int32 {
	ep := n.endpoint(inst, id)
	if ep == nil {
		return codeBadFD
	}
	if len(ep.inbox) == 0 || ep.inbox[0].at > n.w.now {
		return codeAgain
	}
	head := &ep.inbox[0]
	n.seen(ep, head)
	switch head.kind {
	case chunkEOF:
		return codeEOF
	case chunkReset:
		return codeReset
	}
	c := copy(buf, head.data)
	head.data = head.data[c:]
	if len(head.data) == 0 {
		ep.inbox = ep.inbox[1:]
	}
	return int32(c)
}

func (n *network) close(inst *instance, id int32) {
	switch h := n.handles[id].(type) {
	case *listener:
		if h.owner == inst {
			n.closeListener(h)
		}
	case *endpoint:
		if h.owner == inst {
			n.closeEndpoint(h, chunkEOF)
		}
	}
}

func (n *network) closeListener(l *listener) {
	for _, ep := range l.backlog {
		n.closeEndpoint(ep, chunkReset)
	}
	delete(n.listeners, l.key)
	delete(n.handles, l.id)
}

func (n *network) closeEndpoint(ep *endpoint, kind chunkKind) {
	if !ep.gone && !ep.peer.gone {
		if kind == chunkEOF {
			n.dropLastChunk(ep)
		}
		n.send(ep, kind, nil)
	}
	ep.gone = true
	delete(n.handles, ep.id)
}

func (n *network) ids() []int32 {
	ids := make([]int32, 0, len(n.handles))
	for id := range n.handles {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (n *network) reset(inst *instance) int {
	count := 0
	for _, id := range n.ids() {
		switch h := n.handles[id].(type) {
		case *listener:
			if h.owner != inst {
				continue
			}
			h.backlog = slices.DeleteFunc(h.backlog, func(ep *endpoint) bool {
				if ep.acceptAt > n.w.now || ep.gone || ep.peer.gone {
					return false
				}
				n.closeEndpoint(ep, chunkReset)
				count++
				return true
			})
		case *endpoint:
			if h.owner != inst || h.gone || h.peer.gone {
				continue
			}
			n.send(h, chunkReset, nil)
			h.inbox = []chunk{{at: n.w.now, kind: chunkReset}}
			h.gone, h.peer.gone = true, true
			inst.wakeAt = min(inst.wakeAt, n.w.now)
			count++
		}
	}
	return count
}

func (n *network) drop(inst *instance) {
	for _, id := range n.ids() {
		switch h := n.handles[id].(type) {
		case *listener:
			if h.owner == inst {
				n.closeListener(h)
			}
		case *endpoint:
			if h.owner == inst {
				n.closeEndpoint(h, chunkReset)
			}
		}
	}
}

func (w *world) netLine(inst *instance, format string, args ...any) {
	if w.netTrace {
		w.trace.about(w.now, instLine(inst), format, args...)
	}
}

const hostsPath = "etc/hosts"

func (n *network) hostsFile() *memFile {
	in := &inode{data: n.hosts, durable: n.hosts, mtime: epochNanos}
	return &memFile{fs: newMemFS(func() int64 { return epochNanos }), path: hostsPath, in: in}
}
