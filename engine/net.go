package engine

import (
	"slices"
	"strings"
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
}

type endpoint struct {
	id       int32
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
}

func newNetwork(w *world) *network {
	return &network{w: w, listeners: map[string]*listener{}, handles: map[int32]any{}}
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
	if host != "" && host != inst.node.name() {
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
	host, _ := splitHostPort(addr)
	l, ok := n.listeners[addr]
	if !ok || n.w.cut(inst.node.name(), host) {
		return codeRefused
	}
	client := &endpoint{id: n.alloc(), owner: inst}
	server := &endpoint{owner: l.owner, peer: client, acceptAt: n.w.now + n.w.latency()}
	client.peer = server
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
	at := n.w.now + n.w.latency()
	if heal, cut := n.w.cutUntil(from.owner.node.name(), to.owner.node.name()); cut {
		at = max(at, heal+n.w.latency())
	}
	at = max(at, from.lastSent)
	from.lastSent = at
	to.inbox = append(to.inbox, chunk{at: at, kind: kind, data: data})
	to.owner.wakeAt = min(to.owner.wakeAt, at)
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
		n.send(ep, kind, nil)
	}
	ep.gone = true
	delete(n.handles, ep.id)
}

func (n *network) drop(inst *instance) {
	ids := make([]int32, 0, len(n.handles))
	for id := range n.handles {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
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
