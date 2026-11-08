package engine

import (
	"fmt"
	"maps"
	"slices"
	"strings"
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

func (k PropertyKind) messageArg() int {
	if k == Reachable || k == Unreachable {
		return 0
	}
	return 1
}

type Property struct {
	Kind PropertyKind
	Hits int
	True int
}

type properties map[string]*Property

func (p properties) observe(kind PropertyKind, cond bool, msg string) {
	prop, ok := p[msg]
	if !ok {
		prop = &Property{Kind: kind}
		p[msg] = prop
	}
	prop.Hits++
	if cond {
		prop.True++
	}
}

type PropertyCoverage struct {
	Kind         PropertyKind
	Location     string
	SeedsReached int
	SeedsTrue    int
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

func (w *world) observeProperty(inst *instance, kind PropertyKind, cond bool, msg, detail string) {
	if kind < Always || kind > Eventually {
		w.fail(Failure{Class: FailAssert, Node: inst.node.name()}, fmt.Sprintf("%s: unknown property kind %d", inst.label, kind))
		panic(errFailed)
	}
	w.props.observe(kind, cond, msg)
	if kind.violatedBy(cond) {
		w.fail(Failure{Class: kind.failureClass(), Node: inst.node.name(), Property: msg}, strings.TrimSuffix(fmt.Sprintf("%s: %s: %s", inst.label, msg, detail), ": "))
		panic(errFailed)
	}
}
