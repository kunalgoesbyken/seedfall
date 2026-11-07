package engine

func (w *world) onDiskOp(n *node) {
	inst := n.live
	n.ops++
	until, crash := w.ioCrashDue(n)
	if !crash {
		return
	}
	w.trace.add(w.now, "fault crash %s during disk op %d, restart at %s", inst.label, n.ops, fmtTime(until))
	inst.killed, inst.crashUntil = true, until
	panic(errKilled)
}

func (w *world) ioCrashDue(n *node) (int64, bool) {
	delay, crash := w.ioCrashDelay(n)
	if !crash {
		return 0, false
	}
	until := w.now + delay
	w.ioFaults = append(w.ioFaults, Fault{At: Duration(w.now), Kind: FaultCrashIO, Node: n.name(), Op: n.ops, Until: Duration(until)})
	return until, true
}

func (w *world) ioCrashDelay(n *node) (int64, bool) {
	if w.explicit {
		f, ok := w.plannedIOFault(n, FaultCrashIO)
		return int64(f.Until - f.At), ok
	}
	if !w.draw(w.spec.CrashOnIO) {
		return 0, false
	}
	return int64(uniform(w.ioRand, w.spec.RestartAfter)), true
}

func (w *world) fsyncFails(n *node) bool {
	fails := w.draw(w.spec.FsyncEIO)
	if w.explicit {
		_, fails = w.plannedIOFault(n, FaultFsyncEIO)
	}
	if !fails {
		return false
	}
	w.ioFaults = append(w.ioFaults, Fault{At: Duration(w.now), Kind: FaultFsyncEIO, Node: n.name(), Op: n.ops})
	w.trace.add(w.now, "fault fsync EIO on %s at disk op %d, unsynced writes dropped", n.live.label, n.ops)
	return true
}

func (w *world) draw(rate float64) bool {
	return !w.explicit && rate > 0 && !w.faultsSuppressed() && w.ioRand.Float64() < rate
}

func (w *world) plannedIOFault(n *node, kind string) (Fault, bool) {
	for _, f := range w.faults {
		if f.Kind == kind && f.Node == n.name() && f.Op == n.ops {
			return f, true
		}
	}
	return Fault{}, false
}

func (w *world) clockSkew(n *node) int64 {
	limit := int64(w.spec.ClockSkew)
	if limit <= 0 || !n.crashable {
		return 0
	}
	skew := streams(w.seed, "clock/"+n.name()).Int64N(2*limit+1) - limit
	w.trace.add(0, "clock %s runs %s off true time", n.name(), fmtSigned(skew))
	return skew
}

func fmtSigned(ns int64) string {
	if ns < 0 {
		return "-" + fmtTime(-ns)
	}
	return "+" + fmtTime(ns)
}
