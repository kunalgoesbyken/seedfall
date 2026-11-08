package engine

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
)

type PropertyKind int32

const (
	Always PropertyKind = iota
	Sometimes
	Reachable
	Unreachable
	AlwaysOrUnreachable
	Eventually
)

var kindNames = [...]string{"always", "sometimes", "reachable", "unreachable", "always_or_unreachable", "eventually"}

func (k PropertyKind) String() string { return kindNames[k] }

func (k PropertyKind) violatedBy(cond bool) bool {
	return ((k == Always || k == AlwaysOrUnreachable || k == Eventually) && !cond) || k == Unreachable
}

func (k PropertyKind) failureClass() FailureClass {
	if k == Eventually {
		return FailLiveness
	}
	return FailProperty
}

func (k PropertyKind) needsWitness() bool { return k == Sometimes || k == Reachable }

func (k PropertyKind) mustBeReached() bool { return k != Unreachable && k != AlwaysOrUnreachable }

func (k PropertyKind) closer(margin *float64, m float64) *float64 {
	switch {
	case margin == nil:
	case k.needsWitness():
		m = max(*margin, m)
	default:
		m = min(*margin, m)
	}
	return &m
}

func (k PropertyKind) MarginNote(margin *float64) string {
	switch {
	case margin == nil:
		return ""
	case k.needsWitness():
		return fmt.Sprintf("largest margin %g", *margin)
	}
	return fmt.Sprintf("smallest margin %g", *margin)
}

type Property struct {
	Kind   PropertyKind
	Hits   int
	True   int
	Margin *float64
}

type properties map[string]*Property

func (p properties) entry(kind PropertyKind, msg string) *Property {
	prop, ok := p[msg]
	if !ok {
		prop = &Property{Kind: kind}
		p[msg] = prop
	}
	return prop
}

func (p properties) observe(kind PropertyKind, cond bool, msg string) {
	prop := p.entry(kind, msg)
	prop.Hits++
	if cond {
		prop.True++
	}
}

func (p properties) margin(kind PropertyKind, msg string, m float64) {
	prop := p.entry(kind, msg)
	if !math.IsNaN(m) && !math.IsInf(m, 0) {
		prop.Margin = prop.Kind.closer(prop.Margin, m)
	}
}

type PropertyCoverage struct {
	Kind         PropertyKind
	Location     string
	SeedsReached int
	SeedsTrue    int
	Margin       *float64
}

func (c PropertyCoverage) Problem() string {
	switch {
	case c.Kind.mustBeReached() && c.SeedsReached == 0:
		return "never reached"
	case c.Kind.needsWitness() && c.SeedsTrue == 0:
		return "never true"
	}
	return ""
}

func (c PropertyCoverage) Unmet() bool { return c.Problem() != "" }

type Coverage map[string]*PropertyCoverage

func newCoverage(entries []CatalogEntry) Coverage {
	c := Coverage{}
	for _, e := range entries {
		if _, ok := c[e.Message]; !ok && !e.Buggify {
			c[e.Message] = &PropertyCoverage{Kind: e.Kind, Location: e.Location}
		}
	}
	return c
}

func (c Coverage) add(props properties) {
	for msg, p := range props {
		cov, ok := c[msg]
		if !ok {
			cov = &PropertyCoverage{Kind: p.Kind}
			c[msg] = cov
		}
		cov.SeedsReached++
		if p.True > 0 {
			cov.SeedsTrue++
		}
		if p.Margin != nil {
			cov.Margin = cov.Kind.closer(cov.Margin, *p.Margin)
		}
	}
}

func (c Coverage) Messages() []string { return slices.Sorted(maps.Keys(c)) }

func (c Coverage) Unmet() []string {
	var out []string
	for _, msg := range c.Messages() {
		if c[msg].Unmet() {
			out = append(out, msg)
		}
	}
	return out
}

func (w *world) knownKind(inst *instance, kind PropertyKind) {
	if kind < Always || kind > Eventually {
		w.fail(Failure{Class: FailAssert, Node: inst.node.name()}, fmt.Sprintf("%s: unknown property kind %d", inst.label, kind))
		panic(errFailed)
	}
}

func (w *world) observeNumeric(inst *instance, kind PropertyKind, cond bool, margin float64, msg, detail string) {
	w.knownKind(inst, kind)
	w.props.margin(kind, msg, margin)
	w.observeProperty(inst, kind, cond, msg, detail)
}

func (w *world) observeProperty(inst *instance, kind PropertyKind, cond bool, msg, detail string) {
	w.knownKind(inst, kind)
	w.props.observe(kind, cond, msg)
	w.feedback.property(w, msg, cond)
	w.rec.property(inst, w.now, kind, cond, msg, w.props[msg])
	if kind.violatedBy(cond) {
		w.fail(Failure{Class: kind.failureClass(), Node: inst.node.name(), Property: msg}, strings.TrimSuffix(fmt.Sprintf("%s: %s: %s", inst.label, msg, detail), ": "))
		panic(errFailed)
	}
}

type Event struct {
	At       time.Duration
	Instance string
	Name     string
	Details  string
}

func (w *world) event(inst *instance, name, details string) {
	w.sent = append(w.sent, Event{At: time.Duration(w.now), Instance: inst.label, Name: name, Details: details})
	w.trace.about(w.now, instLine(inst), "event %s from %s: %s", name, inst.label, details)
	w.rec.mark(inst, w.now, MarkEvent, name+": "+details)
}
