package engine

import (
	"context"
	"encoding/binary"
	"io/fs"
	"math"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	fsapi "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	never         = int64(math.MaxInt64)
	netFilePrefix = ".seedfall/conn/"

	eventClock   = 0
	eventFdRead  = 1
	eventFdWrite = 2

	subscriptionSize = 48
	eventSize        = 32
	errnoSuccess     = 0
	errnoFault       = 21
	errnoIO          = 29
	errnoNoSpace     = 51
	clockAbstime     = 1
)

type subscription struct {
	userData uint64
	kind     byte
	fd       int32
	deadline int64
}

func wasiModule(ctx context.Context, rt wazero.Runtime) error {
	b := rt.NewHostModuleBuilder(wasi_snapshot_preview1.ModuleName)
	wasi_snapshot_preview1.NewFunctionExporter().ExportFunctions(b)
	if err := reportNoSpace(ctx, rt, b); err != nil {
		return err
	}
	i32 := api.ValueTypeI32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, m api.Module, stack []uint64) { worldOf(ctx).pollOneoff(ctx, m, stack) }), []api.ValueType{i32, i32, i32, i32}, []api.ValueType{i32}).
		Export("poll_oneoff")
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, m api.Module, stack []uint64) { worldOf(ctx).schedYield(ctx, m, stack) }), nil, []api.ValueType{i32}).
		Export("sched_yield")
	_, err := b.Instantiate(ctx)
	return err
}

func (w *world) pollOneoff(ctx context.Context, m api.Module, stack []uint64) {
	in, out, count, resultPtr := uint32(stack[0]), uint32(stack[1]), uint32(stack[2]), uint32(stack[3])
	inst := w.instance(ctx)
	subs, ok := w.readSubscriptions(m, in, count, inst.node)
	if !ok {
		stack[0] = errnoFault
		return
	}
	for {
		ready := w.readySubscriptions(inst, subs)
		if len(ready) > 0 || len(subs) == 0 {
			stack[0] = writeEvents(m, out, resultPtr, ready)
			return
		}
		w.park(inst, w.wakeTime(inst, subs))
	}
}

func (w *world) readSubscriptions(m api.Module, in, count uint32, n *node) ([]subscription, bool) {
	buf, ok := m.Memory().Read(in, count*subscriptionSize)
	if !ok {
		return nil, false
	}
	subs := make([]subscription, count)
	for i := range subs {
		b := buf[i*subscriptionSize:]
		s := subscription{userData: binary.LittleEndian.Uint64(b), kind: b[8]}
		switch s.kind {
		case eventClock:
			timeout := int64(binary.LittleEndian.Uint64(b[24:]))
			if binary.LittleEndian.Uint16(b[40:])&clockAbstime != 0 {
				timeout -= epochNanos + w.now
			}
			s.deadline = w.now + w.stretch(timeout, n)
		default:
			s.fd = int32(binary.LittleEndian.Uint32(b[16:]))
		}
		subs[i] = s
	}
	return subs, true
}

func (w *world) readySubscriptions(inst *instance, subs []subscription) []subscription {
	var ready []subscription
	for _, s := range subs {
		if w.subscriptionReady(inst, s) {
			ready = append(ready, s)
		}
	}
	return ready
}

func (w *world) subscriptionReady(inst *instance, s subscription) bool {
	switch s.kind {
	case eventClock:
		return s.deadline <= w.now
	case eventFdRead:
		id, ok := inst.fds[s.fd]
		return !ok || w.net.readable(id)
	}
	return true
}

func (w *world) wakeTime(inst *instance, subs []subscription) int64 {
	wake := never
	for _, s := range subs {
		switch s.kind {
		case eventClock:
			wake = min(wake, s.deadline)
		case eventFdRead:
			wake = min(wake, w.net.nextArrival(inst.fds[s.fd]))
		}
	}
	return wake
}

func writeEvents(m api.Module, out, resultPtr uint32, ready []subscription) uint64 {
	buf := make([]byte, len(ready)*eventSize)
	for i, s := range ready {
		e := buf[i*eventSize:]
		binary.LittleEndian.PutUint64(e, s.userData)
		e[10] = s.kind
	}
	if !m.Memory().Write(out, buf) || !m.Memory().WriteUint32Le(resultPtr, uint32(len(ready))) {
		return errnoFault
	}
	return errnoSuccess
}

func (n *network) readable(id int32) bool {
	switch h := n.handles[id].(type) {
	case *endpoint:
		return len(h.inbox) > 0 && h.inbox[0].at <= n.w.now
	case *listener:
		for _, ep := range h.backlog {
			if ep.acceptAt <= n.w.now {
				return true
			}
		}
		return false
	}
	return true
}

func (n *network) nextArrival(id int32) int64 {
	switch h := n.handles[id].(type) {
	case *endpoint:
		if len(h.inbox) > 0 {
			return h.inbox[0].at
		}
	case *listener:
		at := never
		for _, ep := range h.backlog {
			at = min(at, ep.acceptAt)
		}
		return at
	}
	return never
}

func (w *world) openNetFile(n *node, p string) (fsapi.File, fsapi.Errno, bool) {
	if p == hostsPath {
		return w.net.hostsFile(), 0, true
	}
	if !strings.HasPrefix(p, netFilePrefix) {
		return nil, 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(p, netFilePrefix), 10, 32)
	if err != nil || n.live == nil {
		return nil, fsapi.ENOENT, true
	}
	h, ok := w.net.handles[int32(id)]
	if !ok || owner(h) != n.live {
		return nil, fsapi.ENOENT, true
	}
	return &netFile{w: w, inst: n.live, id: int32(id)}, 0, true
}

func owner(h any) *instance {
	switch h := h.(type) {
	case *endpoint:
		return h.owner
	case *listener:
		return h.owner
	}
	return nil
}

type netFile struct {
	fsapi.UnimplementedFile
	w        *world
	inst     *instance
	id       int32
	nonblock bool
}

func (f *netFile) Stat() (sys.Stat_t, fsapi.Errno) {
	return sys.Stat_t{Mode: fs.ModeDevice | fs.ModeCharDevice | 0o600, Nlink: 1}, 0
}

func (f *netFile) IsDir() (bool, fsapi.Errno) { return false, 0 }
func (f *netFile) IsNonblock() bool           { return f.nonblock }

func (f *netFile) SetNonblock(enable bool) fsapi.Errno {
	f.nonblock = enable
	return 0
}

func (f *netFile) Poll(fsapi.Pflag, int32) (bool, fsapi.Errno) { return false, fsapi.ENOSYS }

func (f *netFile) Read(buf []byte) (int, fsapi.Errno) {
	if _, ok := f.w.net.handles[f.id].(*listener); ok {
		if !f.w.net.readable(f.id) {
			return 0, fsapi.EAGAIN
		}
		if len(buf) > 0 {
			buf[0] = 1
		}
		return min(len(buf), 1), 0
	}
	switch n := f.w.net.read(f.inst, f.id, buf); n {
	case codeAgain:
		return 0, fsapi.EAGAIN
	case codeEOF:
		return 0, 0
	case codeReset:
		return 0, fsapi.EIO
	case codeBadFD:
		return 0, fsapi.EBADF
	default:
		return int(n), 0
	}
}

func (f *netFile) Write(buf []byte) (int, fsapi.Errno) {
	switch n := f.w.net.write(f.inst, f.id, append([]byte(nil), buf...)); n {
	case codeReset:
		return 0, fsapi.EIO
	case codeBadFD:
		return 0, fsapi.EBADF
	default:
		return int(n), 0
	}
}

func (f *netFile) Close() fsapi.Errno {
	for fd, id := range f.inst.fds {
		if id == f.id {
			delete(f.inst.fds, fd)
		}
	}
	f.w.net.close(f.inst, f.id)
	return 0
}
