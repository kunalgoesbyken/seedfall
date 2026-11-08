package engine

import (
	"maps"
	"math/rand/v2"
	"slices"
)

const buggifyEnableRate = 0.25

type BuggifySite struct {
	Enabled bool `json:"enabled"`
	Hits    int  `json:"hits"`
	Fired   int  `json:"fired"`
}

type buggifyState struct {
	sites map[string]*BuggifySite
	hits  map[string]int64
	fire  map[string]*rand.Rand
}

func (w *world) buggify(inst *instance, site string, p float64) bool {
	if !w.spec.Buggify {
		return false
	}
	if w.bugs.sites == nil {
		w.bugs = buggifyState{sites: map[string]*BuggifySite{}, hits: map[string]int64{}, fire: map[string]*rand.Rand{}}
	}
	s, ok := w.bugs.sites[site]
	if !ok {
		s = &BuggifySite{Enabled: streams(w.seed, "buggify-enable/"+site).Float64() < buggifyEnableRate}
		w.bugs.sites[site] = s
		w.trace.about(w.now, instLine(inst), "%s: buggify site %q reached, %s for this run", inst.label, site, map[bool]string{true: "enabled", false: "disabled"}[s.Enabled])
	}
	n := inst.node.name()
	key := n + "/" + site
	w.bugs.hits[key]++
	op := w.bugs.hits[key]
	s.Hits++
	if !w.buggifyFires(s, key, n, site, op, p) {
		return false
	}
	s.Fired++
	w.ioFaults = append(w.ioFaults, Fault{At: Duration(w.now), Kind: FaultBuggify, Node: n, Site: site, Op: op})
	w.trace.about(w.now, instLine(inst), "fault buggify %q fires in %s at hit %d", site, inst.label, op)
	return true
}

func (w *world) buggifyFires(s *BuggifySite, key, node, site string, op int64, p float64) bool {
	if w.explicit {
		_, ok := w.planned(FaultBuggify, node, site, op)
		return ok
	}
	if !s.Enabled || w.faultsSuppressed() {
		return false
	}
	r, ok := w.bugs.fire[key]
	if !ok {
		r = streams(w.streamSeed("buggify"), "buggify-fire/"+key)
		w.bugs.fire[key] = r
	}
	return r.Float64() < p
}

type BuggifyCoverage struct {
	Location     string
	SeedsReached int
	SeedsEnabled int
	SeedsFired   int
	Fired        int
}

func (c BuggifyCoverage) Problem() string {
	switch {
	case c.SeedsReached == 0:
		return "never reached"
	case c.SeedsFired == 0:
		return "never fired"
	}
	return ""
}

type BuggifySites map[string]*BuggifyCoverage

func newBuggifySites(cfg *Config, entries []CatalogEntry) BuggifySites {
	if !cfg.Faults.Buggify {
		return nil
	}
	c := BuggifySites{}
	for _, e := range entries {
		if _, ok := c[e.Message]; e.Buggify && !ok {
			c[e.Message] = &BuggifyCoverage{Location: e.Location}
		}
	}
	return c
}

func (c BuggifySites) add(sites map[string]*BuggifySite) {
	if c == nil {
		return
	}
	for name, s := range sites {
		cov, ok := c[name]
		if !ok {
			cov = &BuggifyCoverage{}
			c[name] = cov
		}
		cov.SeedsReached++
		if s.Enabled {
			cov.SeedsEnabled++
		}
		if s.Fired > 0 {
			cov.SeedsFired++
		}
		cov.Fired += s.Fired
	}
}

func (c BuggifySites) Names() []string { return slices.Sorted(maps.Keys(c)) }
