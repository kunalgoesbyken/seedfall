package engine

import (
	"fmt"
	"slices"
	"time"
)

func (w *world) setupComplete(inst *instance) {
	if w.setupDone {
		return
	}
	w.setupDone = true
	w.trace.add(w.now, "%s: setup complete, faults enabled", inst.label)
}

func (w *world) quietFor(inst *instance, d time.Duration) {
	w.quietUntil = max(w.quietUntil, w.now+int64(d))
	w.healNetwork()
	w.trace.add(w.now, "%s: quiet period until %s, partitions, clogs and slow nodes healed", inst.label, fmtTime(w.quietUntil))
}

func (w *world) faultsSuppressed() bool {
	return (w.cfg.FaultsAfterSetup && !w.setupDone) || w.now < w.quietUntil
}

func (w *world) quietReason() string {
	if w.recovering {
		return "the recovery phase"
	}
	return "a quiet period"
}

func (w *world) workloadDone(inst *instance) {
	if slices.ContainsFunc(w.nodes, func(n *node) bool { return n.workload && !n.done }) {
		w.trace.add(w.now, "%s finished, waiting for the other workloads", inst.label)
		return
	}
	if w.finally == nil {
		w.finished = true
		w.trace.add(w.now, "workload finished")
		return
	}
	w.trace.add(w.now, "workload finished, the finally check runs next")
	w.recover("the workload finished")
	w.start(w.finally)
}

func (w *world) recover(reason string) {
	if w.recovering {
		return
	}
	w.recovering, w.quietUntil = true, never
	w.healNetwork()
	restarted := 0
	for _, n := range w.nodes {
		n.pausedTo = min(n.pausedTo, w.now)
		if n.live == nil && n.restartAt > w.now {
			n.restartAt = w.now
			restarted++
		}
	}
	msg := fmt.Sprintf("recovery: %s; faults stop, partitions, clogs, slowdowns and pauses end, %d crashed nodes restart now", reason, restarted)
	if within := int64(w.cfg.Liveness.Within); within > 0 {
		w.deadline = w.now + within
		msg += fmt.Sprintf(", everything must finish by %s", fmtTime(w.deadline))
	}
	w.trace.add(w.now, "%s", msg)
}

func (w *world) livenessDue(next int64) bool {
	within := w.cfg.Liveness.Within
	switch {
	case within <= 0:
		return false
	case !w.recovering && next > w.horizon:
		w.now = max(w.now, w.horizon)
		w.recover("the fault period ended")
		return true
	case next > w.deadline:
		w.now = max(w.now, w.deadline)
		n := w.unfinished()
		w.fail(Failure{Class: FailLiveness, Node: n.name()}, fmt.Sprintf("liveness: %s did not finish within %s of the recovery phase starting at %s", n.name(), time.Duration(within), fmtTime(w.deadline-int64(within))))
		return true
	}
	return false
}

func (w *world) unfinished() *node {
	if w.finally != nil && w.finally.starts > 0 {
		return w.finally
	}
	i := slices.IndexFunc(w.nodes, func(n *node) bool { return n.workload && !n.done })
	return w.nodes[i]
}
