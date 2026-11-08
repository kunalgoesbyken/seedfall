package engine

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	fsapi "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

var fileWrites = []string{"fd_write", "fd_pwrite"}

func reportNoSpace(ctx context.Context, rt wazero.Runtime, b wazero.HostModuleBuilder) error {
	plain := rt.NewHostModuleBuilder(wasi_snapshot_preview1.ModuleName)
	wasi_snapshot_preview1.NewFunctionExporter().ExportFunctions(plain)
	compiled, err := plain.Compile(ctx)
	if err != nil {
		return err
	}
	defer compiled.Close(ctx)
	for _, name := range fileWrites {
		def := compiled.ExportedFunctions()[name]
		write, ok := def.GoFunction().(api.GoModuleFunction)
		if !ok {
			return fmt.Errorf("wasi %s is not a Go module function", name)
		}
		b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, m api.Module, stack []uint64) {
			fs := worldOf(ctx).instance(ctx).node.fs
			fs.noSpace = false
			write.Call(ctx, m, stack)
			if fs.noSpace && stack[0] == errnoIO {
				stack[0] = errnoNoSpace
			}
		}), def.ParamTypes(), def.ResultTypes()).Export(name)
	}
	return nil
}

type ratedFault struct {
	kind string
	rate float64
}

func (w *world) wireDisk(n *node) {
	n.fs.capacity = w.spec.DiskBytes
	n.fs.writeFails = func(p string) fsapi.Errno { return w.writeFails(n, p) }
	n.fs.readFault = func(p string, off int64, size int) (fsapi.Errno, int, byte) { return w.readFault(n, p, off, size) }
	n.fs.syncDamage = func(p string, dirty []int64, pages int64) (Damage, bool) { return w.syncDamage(n, p, dirty, pages) }
	n.fs.syncWait = func() { w.syncWait(n) }
	n.fs.diskFull = func(p string, kept, size int) { w.diskFull(n, p, kept, size) }
}

func (w *world) ioFault(n *node, kind string, rate float64, op int64) bool {
	if w.explicit {
		if w.scheduled == nil {
			w.scheduled = map[string]bool{}
			for _, f := range w.faults {
				w.scheduled[f.Kind] = true
			}
		}
		if !w.scheduled[kind] {
			return false
		}
		_, ok := w.planned(kind, n.name(), "", op)
		return ok
	}
	return w.draw(rate)
}

func (w *world) detail(kind string, n *node, op int64) *rand.Rand {
	return streams(w.streamSeed(), fmt.Sprintf("%s/%s/%d", kind, n.name(), op))
}

func (w *world) recordDisk(f Fault) {
	f.At = Duration(w.now)
	w.ioFaults = append(w.ioFaults, f)
	w.trace.about(w.now, nodeLine(f.Node), "fault %s", f.diskText())
}

func (w *world) writeFails(n *node, p string) fsapi.Errno {
	op := n.ops + 1
	if w.ioFault(n, FaultWriteEIO, w.spec.WriteEIO, op) {
		w.recordDisk(Fault{Kind: FaultWriteEIO, Node: n.name(), Op: op, Path: p})
		return fsapi.EIO
	}
	if w.ioFault(n, FaultENOSPC, w.spec.WriteENOSPC, op) {
		w.recordDisk(Fault{Kind: FaultENOSPC, Node: n.name(), Op: op, Path: p})
		return n.fs.noSpaceLeft()
	}
	return 0
}

func (w *world) readFault(n *node, p string, off int64, size int) (fsapi.Errno, int, byte) {
	n.reads++
	if w.ioFault(n, FaultReadEIO, w.spec.ReadEIO, n.reads) {
		w.recordDisk(Fault{Kind: FaultReadEIO, Node: n.name(), Op: n.reads, Path: p})
		return fsapi.EIO, 0, 0
	}
	if size == 0 || !w.ioFault(n, FaultReadCorrupt, w.spec.ReadCorrupt, n.reads) {
		return 0, 0, 0
	}
	r := w.detail(FaultReadCorrupt, n, n.reads)
	i := r.IntN(size)
	w.recordDisk(Fault{Kind: FaultReadCorrupt, Node: n.name(), Op: n.reads, Path: p, Offset: off + int64(i)})
	return 0, i, byte(1) << r.IntN(8)
}

func (w *world) syncDamage(n *node, p string, dirty []int64, pages int64) (Damage, bool) {
	for _, e := range []ratedFault{{FaultLostWrite, w.spec.LostWrite}, {FaultMisdirected, w.spec.MisdirectedWrite}} {
		if !w.ioFault(n, e.kind, e.rate, n.ops) || (e.kind == FaultMisdirected && pages < 2) {
			continue
		}
		r := w.detail(e.kind, n, n.ops)
		d := Damage{Kind: e.kind, Offset: dirty[r.IntN(len(dirty))] * pageSize}
		if e.kind == FaultMisdirected {
			target := r.Int64N(pages - 1)
			if target >= d.Offset/pageSize {
				target++
			}
			d.Target = target * pageSize
		}
		w.recordDisk(Fault{Kind: e.kind, Node: n.name(), Op: n.ops, Path: p, Offset: d.Offset, Target: d.Target})
		return d, true
	}
	return Damage{}, false
}

func (w *world) syncWait(n *node) {
	d := uniform(w.latRand, w.spec.DiskLatency)
	if d <= 0 {
		return
	}
	w.park(n.live, w.now+w.stretch(int64(d), n))
}

func (w *world) diskFull(n *node, p string, kept, size int) {
	w.trace.about(w.now, instLine(n.live), "disk full on %s: %d of a %d-byte write to %s fit, the rest fails with ENOSPC (%d of %d bytes used)", n.live.label, kept, size, p, n.fs.used(), n.fs.capacity)
}

func (w *world) rot(f *Fault) {
	n := w.nodeByName(f.Node)
	if n == nil || !n.crashable {
		return
	}
	p, d, ok := n.fs.rot(streams(w.streamSeed(), fmt.Sprintf("%s/%s/%d", FaultBitrot, f.Node, f.At)))
	if !ok {
		w.trace.about(w.now, nodeLine(f.Node), "fault bit rot on %s: nothing durable to corrupt", f.Node)
		return
	}
	f.Path, f.Offset = p, d.Offset
	w.trace.about(w.now, nodeLine(f.Node), "fault %s", f.diskText())
}

func (w *world) traceDamage(n *node, damaged []fileDamage) {
	for _, d := range damaged {
		w.trace.about(w.now, nodeLine(n.name()), "crash corrupted the half-written byte at offset %d of %s on %s", d.Offset, d.path, n.name())
	}
}

func (f Fault) diskText() string {
	switch f.Kind {
	case FaultWriteEIO:
		return fmt.Sprintf("write to %s on %s fails with EIO at disk op #%d", f.Path, f.Node, f.Op)
	case FaultENOSPC:
		return fmt.Sprintf("write to %s on %s fails with ENOSPC at disk op #%d", f.Path, f.Node, f.Op)
	case FaultReadEIO:
		return fmt.Sprintf("read of %s on %s fails with EIO at disk read #%d", f.Path, f.Node, f.Op)
	case FaultReadCorrupt:
		return fmt.Sprintf("read of %s on %s at disk read #%d returns a flipped bit at offset %d, the stored data is intact", f.Path, f.Node, f.Op, f.Offset)
	case FaultLostWrite:
		return fmt.Sprintf("fsync of %s on %s at disk op #%d succeeds, but the page at offset %d never reaches the disk", f.Path, f.Node, f.Op, f.Offset)
	case FaultMisdirected:
		return fmt.Sprintf("fsync of %s on %s at disk op #%d succeeds, but the page for offset %d lands at offset %d", f.Path, f.Node, f.Op, f.Offset, f.Target)
	case FaultBitrot:
		if f.Path == "" {
			return fmt.Sprintf("bit rot flips a stored bit on %s", f.Node)
		}
		return fmt.Sprintf("bit rot flips a stored bit at offset %d of %s on %s", f.Offset, f.Path, f.Node)
	}
	return ""
}
