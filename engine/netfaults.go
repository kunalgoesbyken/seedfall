package engine

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

const (
	TopologyHalf     = "half"
	TopologyOne      = "one"
	TopologyMajority = "majority"
	TopologyRing     = "majorities_ring"
	TopologyGroups   = "random_groups"

	ClogStop = "stop"
	ClogSlow = "slow"
	ClogJam  = "jam"

	DirIn   = "in"
	DirOut  = "out"
	DirBoth = "both"

	splitNodes = 2
)

var (
	topologies = []string{TopologyHalf, TopologyOne, TopologyMajority, TopologyRing, TopologyGroups}
	clogModes  = []string{ClogStop, ClogSlow, ClogJam}
	clogDirs   = []string{DirIn, DirOut, DirBoth}
	minNodes   = map[string]int{TopologyMajority: 3, TopologyRing: 4}
)

func (c *Config) validateNetwork(names map[string]bool) error {
	known := func(field, name string) error {
		if name != "" && !names[name] {
			return fmt.Errorf("config: %s names unknown node %q", field, name)
		}
		return nil
	}
	for i, l := range c.Links {
		if err := errors.Join(known(fmt.Sprintf("links[%d].from", i), l.From), known(fmt.Sprintf("links[%d].to", i), l.To)); err != nil {
			return err
		}
	}
	f := c.Faults
	if err := oneOf("faults.partition_topologies", f.PartitionTopologies, topologies); err != nil {
		return err
	}
	if err := oneOf("faults.clog_modes", f.ClogModes, clogModes); err != nil {
		return err
	}
	targets := len(nodeNames(c))
	if max(f.PartitionPerSec, f.ClogPerSec, f.SwizzlePerSec) > 0 && targets < splitNodes {
		return fmt.Errorf("config: partitions, clogs and swizzles need at least %d nodes that take faults, workload included", splitNodes)
	}
	for _, t := range f.PartitionTopologies {
		if f.PartitionPerSec > 0 && targets < minNodes[t] {
			return fmt.Errorf("config: partition topology %q needs at least %d nodes that take faults, workload included", t, minNodes[t])
		}
	}
	if f.ClogPerSec > 0 && len(f.ClogModes) == 0 {
		return errors.New("config: faults.clog_modes is empty")
	}
	if f.SlowPerSec > 0 && f.SlowFactor <= 1 {
		return errors.New("config: faults.slow_factor must be greater than 1")
	}
	return nil
}

func oneOf(field string, got, allowed []string) error {
	for _, v := range got {
		if !slices.Contains(allowed, v) {
			return fmt.Errorf("config: %s has unknown value %q, want one of %s", field, v, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func pick[T any](r *rand.Rand, from []T) T {
	if len(from) == 1 {
		return from[0]
	}
	return from[r.IntN(len(from))]
}

func randomPartition(r *rand.Rand, names, kinds []string) Fault {
	if len(kinds) == 0 {
		kinds = []string{TopologyHalf}
	}
	switch pick(r, kinds) {
	case TopologyOne:
		return Fault{Kind: FaultPartition, Side: []string{pick(r, names)}}
	case TopologyMajority:
		return Fault{Kind: FaultPartition, Side: inOrder(names, r.Perm(len(names))[:len(names)/2+1])}
	case TopologyRing:
		return Fault{Kind: FaultPartition, Groups: majoritiesRing(r, names)}
	case TopologyGroups:
		return Fault{Kind: FaultPartition, Groups: randomGroups(r, names)}
	}
	return Fault{Kind: FaultPartition, Side: randomSide(r, names)}
}

func inOrder(names []string, picked []int) []string {
	slices.Sort(picked)
	out := make([]string, len(picked))
	for i, p := range picked {
		out[i] = names[p]
	}
	return out
}

func majoritiesRing(r *rand.Rand, names []string) [][]string {
	ring := r.Perm(len(names))
	width := (len(names)/2 + 3) / 2
	groups := make([][]string, len(ring))
	for i := range ring {
		var window []int
		for k := range width {
			window = append(window, ring[(i+k)%len(ring)])
		}
		groups[i] = inOrder(names, window)
	}
	return groups
}

func randomGroups(r *rand.Rand, names []string) [][]string {
	for {
		k := 2 + r.IntN(len(names)-1)
		groups := make([][]string, k)
		for _, n := range names {
			g := r.IntN(k)
			groups[g] = append(groups[g], n)
		}
		groups = slices.DeleteFunc(groups, func(g []string) bool { return len(g) == 0 })
		if len(groups) > 1 {
			return groups
		}
	}
}

func randomClog(r *rand.Rand, names []string, spec FaultSpec) Fault {
	f := Fault{Kind: FaultClog, Mode: pick(r, spec.ClogModes)}
	if f.Mode == ClogSlow {
		f.Delay = Duration(uniform(r, spec.ClogDelay))
	}
	if shape := r.IntN(len(clogDirs) + 1); shape < len(clogDirs) {
		f.Node, f.Dir = pick(r, names), clogDirs[shape]
		return f
	}
	link := r.Perm(len(names))
	f.From, f.To = names[link[0]], names[link[1]]
	return f
}

func swizzle(r *rand.Rand, t time.Duration, names []string, spec FaultSpec) ([]Fault, time.Duration) {
	span := uniform(r, spec.ClogFor)
	nodes := randomSide(r, names)
	step := span / time.Duration(len(nodes))
	clogged, unclogged := r.Perm(len(nodes)), r.Perm(len(nodes))
	out := make([]Fault, len(nodes))
	for i, n := range clogged {
		out[n] = Fault{At: Duration(t + time.Duration(i)*step), Kind: FaultClog, Node: nodes[n], Dir: DirBoth, Mode: ClogStop}
	}
	for i, n := range unclogged {
		out[n].Until = Duration(t + span + time.Duration(i)*step)
	}
	return out, t + 2*span
}

func slowFactor(r *rand.Rand, max float64) float64 {
	return 1 + math.Round((max-1)*r.Float64()*10)/10
}

func (f Fault) affects(from, to string) bool {
	if from == to {
		return false
	}
	switch f.Kind {
	case FaultPartition:
		if len(f.Groups) == 0 {
			return slices.Contains(f.Side, from) != slices.Contains(f.Side, to)
		}
		return !slices.ContainsFunc(f.Groups, func(g []string) bool { return slices.Contains(g, from) && slices.Contains(g, to) })
	case FaultClog:
		switch f.Dir {
		case "":
			return from == f.From && to == f.To
		case DirIn:
			return to == f.Node
		case DirOut:
			return from == f.Node
		}
		return from == f.Node || to == f.Node
	}
	return false
}

func (f Fault) mode() string {
	if f.Kind == FaultClog {
		return f.Mode
	}
	return ClogStop
}

type linkState struct {
	refused bool
	held    int64
	delay   int64
}

func (w *world) link(from, to string) linkState {
	var s linkState
	if w.shielded[from] || w.shielded[to] {
		return s
	}
	for _, f := range w.netFaults {
		if int64(f.Until) <= w.now || !f.affects(from, to) {
			continue
		}
		switch f.mode() {
		case ClogSlow:
			s.delay += int64(f.Delay)
		case ClogJam:
			s.held = max(s.held, int64(f.Until))
		default:
			s.held, s.refused = max(s.held, int64(f.Until)), true
		}
	}
	return s
}

func (w *world) cut(a, b string) bool {
	return w.link(a, b).refused || w.link(b, a).refused
}

func (w *world) latency(from, to string) int64 {
	rg := w.cfg.Latency
	for _, l := range w.cfg.Links {
		if (l.From == "" || l.From == from) && (l.To == "" || l.To == to) {
			rg = l.Latency
			break
		}
	}
	return w.stretch(int64(uniform(w.netRand, rg)), w.nodeByName(from), w.nodeByName(to))
}

func (w *world) slowness(n *node) float64 {
	if n == nil || w.now >= n.slowUntil {
		return 1
	}
	return n.slowFactor
}

func (w *world) stretch(d int64, nodes ...*node) int64 {
	factor := 1.0
	for _, n := range nodes {
		factor = max(factor, w.slowness(n))
	}
	if d <= 0 || factor == 1 {
		return d
	}
	return int64(min(float64(d)*factor, float64(never/2)))
}

func (w *world) applyNetFault(f Fault) {
	w.netFaults = slices.DeleteFunc(w.netFaults, func(a Fault) bool {
		return int64(a.Until) <= w.now || (f.Kind == FaultPartition && a.Kind == FaultPartition)
	})
	w.netFaults = append(w.netFaults, f)
}

func (w *world) healNetwork() {
	for i := range w.netFaults {
		w.netFaults[i].Until = min(w.netFaults[i].Until, Duration(w.now))
	}
	for _, n := range w.nodes {
		n.slowUntil = min(n.slowUntil, w.now)
	}
}

func fmtGroups(groups [][]string) string {
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = fmt.Sprint(g)
	}
	return strings.Join(parts, " ")
}

func (f Fault) clogTarget() string {
	target := fmt.Sprintf("%s -> %s", f.From, f.To)
	switch f.Dir {
	case DirIn:
		target = f.Node + " inbound"
	case DirOut:
		target = f.Node + " outbound"
	case DirBoth:
		target = f.Node + " inbound and outbound"
	}
	if f.Mode == ClogSlow {
		return fmt.Sprintf("%s (slow, +%s per message)", target, time.Duration(f.Delay))
	}
	return fmt.Sprintf("%s (%s)", target, f.Mode)
}

func (f Fault) partitionText() string {
	if len(f.Groups) > 0 {
		return "partition into " + fmtGroups(f.Groups) + ", nodes talk only within a group,"
	}
	return fmt.Sprintf("partition %v from the rest", f.Side)
}
