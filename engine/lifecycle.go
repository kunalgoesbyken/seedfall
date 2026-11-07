package engine

import "time"

func (w *world) setupComplete(inst *instance) {
	if w.setupDone {
		return
	}
	w.setupDone = true
	w.trace.add(w.now, "%s: setup complete, faults enabled", inst.label)
}

func (w *world) quietFor(inst *instance, d time.Duration) {
	w.quietUntil = max(w.quietUntil, w.now+int64(d))
	if w.part != nil && w.part.until > w.now {
		w.part.until = w.now
	}
	w.trace.add(w.now, "%s: quiet period until %s, partitions healed", inst.label, fmtTime(w.quietUntil))
}

func (w *world) faultsSuppressed() bool {
	return (w.cfg.FaultsAfterSetup && !w.setupDone) || w.now < w.quietUntil
}
