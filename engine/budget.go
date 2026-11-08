package engine

func (w *world) budget(inst *instance, n int64) int64 {
	w.opsWanted = max(w.opsWanted, n)
	if w.opsLimit <= 0 || n <= w.opsLimit {
		return n
	}
	w.trace.add(w.now, "%s: sim.Budget capped at %d of %d operations", inst.label, w.opsLimit, n)
	return w.opsLimit
}
