package engine

import (
	"fmt"
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
		return codeRefused
	}
	there := n.w.link(from, host)
	acceptAt := n.w.now + n.w.latency(from, host)
	if held := max(there.held, n.w.link(host, from).held); held > 0 {
		acceptAt = max(acceptAt, held+n.w.latency(from, host))
	}
	acceptAt += there.delay
	client := &endpoint{id: n.alloc(), owner: inst}
	server := &endpoint{owner: l.owner, peer: client, acceptAt: acceptAt}
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
	src, dst := from.owner.node.name(), to.owner.node.name()
	link := n.w.link(src, dst)
	at := n.w.now + n.w.latency(src, dst)
	if link.held > 0 {
		at = max(at, link.held+n.w.latency(src, dst))
	}
	at += link.delay
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
		ep, ok := n.handles[id].(*endpoint)
		if !ok || ep.owner != inst || ep.gone || ep.peer.gone {
			continue
		}
		n.send(ep, chunkReset, nil)
		ep.inbox = []chunk{{at: n.w.now, kind: chunkReset}}
		ep.gone, ep.peer.gone = true, true
		inst.wakeAt = min(inst.wakeAt, n.w.now)
		count++
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

const hostsPath = "etc/hosts"

func (n *network) hostsFile() *memFile {
	in := &inode{data: n.hosts, durable: n.hosts, mtime: epochNanos}
	return &memFile{fs: newMemFS(func() int64 { return epochNanos }), path: hostsPath, in: in}
}
