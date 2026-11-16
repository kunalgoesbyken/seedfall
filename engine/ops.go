package engine

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

const (
	OpCrash     = "crash"
	OpRestart   = "restart"
	OpPause     = "pause"
	OpResume    = "resume"
	OpPartition = "partition"
	OpHeal      = "heal"
	OpSlow      = "slow"
	OpClockJump = "clock_jump"
	OpSetArgs   = "set_args"
	OpUpgrade   = "upgrade"
	OpWait      = "wait"
)

var (
	OpKinds       = []string{OpCrash, OpRestart, OpPause, OpResume, OpPartition, OpHeal, OpSlow, OpClockJump, OpSetArgs, OpUpgrade, OpWait}
	clusterOps    = []string{OpPartition, OpHeal, OpWait}
	crashingOps   = []string{OpCrash, OpRestart, OpUpgrade}
	errOpArgCount = errors.New("wrong number of args")
)

type Op struct {
	At   Duration `json:"at"`
	Kind string   `json:"kind"`
	Node string   `json:"node,omitempty"`
	Args []string `json:"args,omitempty" jsonschema:"restart: optional delay before the node starts again; partition: how long, then the nodes of one side; slow: factor, then how long; clock_jump: the shift; set_args: the node's whole new argument list; wait: how long"`
}

type opPlan struct {
	span   time.Duration
	shift  time.Duration
	factor float64
	side   []string
}

func parseSpan(s string, floor time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err == nil && d < floor {
		err = fmt.Errorf("%s is below %s", s, floor)
	}
	return d, err
}

func (o Op) plan() (p opPlan, err error) {
	args := o.Args
	wrong := func(want int) bool { return len(args) != want }
	switch o.Kind {
	case OpRestart:
		if len(args) > 1 {
			return p, errOpArgCount
		}
		if len(args) == 1 {
			p.span, err = parseSpan(args[0], 0)
		}
	case OpPartition:
		if len(args) < 2 {
			return p, errOpArgCount
		}
		p.span, err = parseSpan(args[0], 1)
		p.side = args[1:]
	case OpSlow:
		if wrong(2) {
			return p, errOpArgCount
		}
		if p.factor, err = strconv.ParseFloat(args[0], 64); err == nil && p.factor < 1 {
			err = fmt.Errorf("factor %s is below 1", args[0])
		}
		if err == nil {
			p.span, err = parseSpan(args[1], 1)
		}
	case OpClockJump:
		if wrong(1) {
			return p, errOpArgCount
		}
		p.shift, err = time.ParseDuration(args[0])
	case OpWait:
		if wrong(1) {
			return p, errOpArgCount
		}
		p.span, err = parseSpan(args[0], 1)
	case OpSetArgs:
	default:
		if len(args) > 0 {
			return p, errOpArgCount
		}
	}
	return p, err
}

func (o Op) problems(names []string, crashable, upgradable func(string) bool, horizon time.Duration) []string {
	if !slices.Contains(OpKinds, o.Kind) {
		return []string{fmt.Sprintf("unknown kind, want one of %v", OpKinds)}
	}
	var out []string
	add := func(bad bool, format string, args ...any) {
		if bad {
			out = append(out, fmt.Sprintf(format, args...))
		}
	}
	add(o.At < 0, "at is negative")
	add(time.Duration(o.At) > horizon, "at is after the end of the run at %s, so it never fires", horizon)
	needsNode := !slices.Contains(clusterOps, o.Kind)
	add(needsNode && o.Node == "", "node is required")
	add(!needsNode && o.Node != "", "%s takes no node", o.Kind)
	for _, n := range append([]string{o.Node}, o.plannedSide()...) {
		add(n != "" && !slices.Contains(names, n), "unknown node %q, want one of %v", n, names)
	}
	add(o.Node != "" && slices.Contains(crashingOps, o.Kind) && !crashable(o.Node), "%s takes no faults, so it cannot be %s", o.Node, o.Kind)
	add(o.Kind == OpUpgrade && o.Node != "" && !upgradable(o.Node), "%s has no upgrade_to", o.Node)
	if _, err := o.plan(); err != nil {
		out = append(out, err.Error())
	}
	return out
}

func (o Op) plannedSide() []string {
	if o.Kind == OpPartition && len(o.Args) > 1 {
		return o.Args[1:]
	}
	return nil
}

func (c *Config) checkOps(ops []Op, horizon time.Duration) error {
	names := namesWhere(c, func(NodeSpec) bool { return true })
	specs := map[string]NodeSpec{}
	for _, s := range c.all() {
		specs[s.Name] = s
	}
	crashable := func(n string) bool { return c.faultable(specs[n]) }
	upgradable := func(n string) bool { return specs[n].UpgradeTo != "" }
	var errs []error
	for i, o := range ops {
		if i > 0 && o.At < ops[i-1].At {
			errs = append(errs, fmt.Errorf("operator action %d (%s at %s): at is before the previous action", i+1, o.Kind, time.Duration(o.At)))
		}
		for _, problem := range o.problems(names, crashable, upgradable, horizon) {
			errs = append(errs, fmt.Errorf("operator action %d (%s at %s): %s", i+1, o.Kind, time.Duration(o.At), problem))
		}
	}
	return errors.Join(errs...)
}

func (w *world) operable() bool {
	return w.nextOp < len(w.ops) || (w.debug != nil && w.debug.gate)
}

func (w *world) takeDueOp(next int64) bool {
	if w.nextOp >= len(w.ops) || int64(w.ops[w.nextOp].At) >= next {
		return false
	}
	at := w.ops[w.nextOp].At
	for w.nextOp < len(w.ops) && w.ops[w.nextOp].At == at {
		w.operate(w.ops[w.nextOp])
		w.nextOp++
	}
	return true
}

func (w *world) operate(o Op) {
	w.opSeq++
	w.now = max(w.now, int64(o.At))
	w.hostCall(nil, "operator", o.Kind+" "+o.Node, int64(o.At))
	p, err := o.plan()
	n := w.nodeByName(o.Node)
	switch {
	case err != nil:
		w.trace.about(w.now, nodeLine(o.Node), "operator %s rejected: %v", o.Kind, err)
	case o.Node != "" && n == nil:
		w.trace.add(w.now, "operator %s rejected: unknown node %q", o.Kind, o.Node)
	case o.Kind == OpUpgrade && n.upgrade == nil:
		w.trace.about(w.now, nodeLine(o.Node), "operator upgrade rejected: %s has no upgrade_to", o.Node)
	default:
		w.applyOp(o, p, n)
	}
}

func (w *world) applyOp(o Op, p opPlan, n *node) {
	switch o.Kind {
	case OpCrash:
		w.operatorCrash(o, n, never)
	case OpRestart, OpUpgrade:
		w.operatorRestart(o, p, n)
	case OpPause:
		if n.live == nil {
			w.trace.about(w.now, nodeLine(o.Node), "operator pause %s: it is down", o.Node)
			return
		}
		n.pausedTo = never
		w.trace.about(w.now, instLine(n.live), "operator pauses %s", n.live.label)
	case OpResume:
		n.pausedTo = min(n.pausedTo, w.now)
		w.trace.about(w.now, nodeLine(o.Node), "operator resumes %s", o.Node)
	case OpPartition:
		w.inject(&Fault{At: Duration(w.now), Kind: FaultPartition, Side: p.side, Until: Duration(w.now + int64(p.span))})
	case OpHeal:
		w.healNetwork()
		w.trace.add(w.now, "operator heals the network, partitions, clogs and slow nodes end")
	case OpSlow:
		w.inject(&Fault{At: Duration(w.now), Kind: FaultSlow, Node: o.Node, Factor: p.factor, Until: Duration(w.now + int64(p.span))})
	case OpClockJump:
		w.inject(&Fault{At: Duration(w.now), Kind: FaultClockJump, Node: o.Node, Shift: Duration(p.shift)})
	case OpSetArgs:
		n.spec.Args = slices.Clone(o.Args)
		w.trace.about(w.now, nodeLine(o.Node), "operator sets the args of %s to %v, they apply from its next start", o.Node, o.Args)
	case OpWait:
		w.trace.add(w.now, "operator waits %s", p.span)
	}
}

func (w *world) operatorCrash(o Op, n *node, until int64) {
	switch {
	case !n.crashable:
		w.trace.about(w.now, nodeLine(o.Node), "operator %s %s: it takes no faults", o.Kind, o.Node)
	case n.live == nil:
		w.trace.about(w.now, nodeLine(o.Node), "operator %s %s: it is already down", o.Kind, o.Node)
	default:
		back := "an operator restart"
		if until != never {
			back = fmtTime(until)
		}
		w.trace.about(w.now, instLine(n.live), "operator stops %s, it starts again at %s", n.live.label, back)
		w.kill(n.live)
		w.crashDisk(n, until, fmt.Sprintf("operator/%d", o.At))
	}
}

func (w *world) operatorRestart(o Op, p opPlan, n *node) {
	until := w.now + int64(p.span)
	switch {
	case n.live != nil:
		w.operatorCrash(o, n, until)
	case !n.crashable:
		w.trace.about(w.now, nodeLine(o.Node), "operator %s %s: it takes no faults", o.Kind, o.Node)
	default:
		n.restartAt = until
		w.trace.about(w.now, nodeLine(o.Node), "operator starts %s at %s", o.Node, fmtTime(until))
	}
}

func sortedOps(ops []Op) []Op {
	out := slices.Clone(ops)
	slices.SortStableFunc(out, func(a, b Op) int { return cmp.Compare(a.At, b.At) })
	return out
}
