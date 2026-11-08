package engine

import (
	"fmt"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
	"time"
)

func buggifyWorld(seed uint64) (*world, *instance) {
	cfg := defaultConfig()
	w := &world{cfg: &cfg, seed: seed, trace: &tracer{}, spec: FaultSpec{Buggify: true}}
	return w, &instance{node: &node{spec: NodeSpec{Name: "n1"}}, label: "n1#1"}
}

func hits(w *world, inst *instance, site string, p float64, n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = w.buggify(inst, site, p)
	}
	return out
}

func TestBuggifyIsOffUnlessConfigured(t *testing.T) {
	w, inst := buggifyWorld(1)
	w.spec.Buggify = false
	for range 100 {
		if w.buggify(inst, "site", 1) {
			t.Fatal("a site fired with buggify off")
		}
	}
	if w.bugs.sites != nil || len(w.trace.lines) != 0 || len(w.ioFaults) != 0 {
		t.Fatal("buggify off still recorded sites, trace lines or faults")
	}
}

func TestBuggifyEnablesAboutAQuarterOfSites(t *testing.T) {
	w, inst := buggifyWorld(7)
	const sites = 2000
	enabled := 0
	for i := range sites {
		name := fmt.Sprintf("site %d", i)
		w.buggify(inst, name, 0)
		if w.bugs.sites[name].Enabled {
			enabled++
		}
	}
	if rate := float64(enabled) / sites; rate < buggifyEnableRate-0.05 || rate > buggifyEnableRate+0.05 {
		t.Fatalf("%.3f of sites enabled, want about %.2f", rate, buggifyEnableRate)
	}
}

func enabledSite(t *testing.T, seed uint64) string {
	t.Helper()
	w, inst := buggifyWorld(seed)
	for i := range 100 {
		name := fmt.Sprintf("site %d", i)
		w.buggify(inst, name, 0)
		if w.bugs.sites[name].Enabled {
			return name
		}
	}
	t.Fatal("no site enabled in 100")
	return ""
}

func TestBuggifySitesDoNotShiftEachOther(t *testing.T) {
	site := enabledSite(t, 3)
	alone, inst := buggifyWorld(3)
	want := hits(alone, inst, site, 0.5, 200)
	crowded, inst := buggifyWorld(3)
	var got []bool
	for i := range 200 {
		crowded.buggify(inst, fmt.Sprintf("other %d", i%7), 0.5)
		got = append(got, crowded.buggify(inst, site, 0.5))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("evaluating other sites changed when this site fires")
	}
}

func TestBuggifyFiresAtTheGivenRateAndRecordsEachFiring(t *testing.T) {
	site := enabledSite(t, 5)
	w, inst := buggifyWorld(5)
	fired := 0
	for _, f := range hits(w, inst, site, 0.3, 4000) {
		if f {
			fired++
		}
	}
	if rate := float64(fired) / 4000; rate < 0.25 || rate > 0.35 {
		t.Fatalf("fired %.3f of hits, want about 0.3", rate)
	}
	s := w.bugs.sites[site]
	if s.Hits != 4000 || s.Fired != fired || len(w.ioFaults) != fired {
		t.Fatalf("site %+v and %d recorded faults, want 4000 hits and %d firings", *s, len(w.ioFaults), fired)
	}
	for _, f := range w.ioFaults {
		if f.Kind != FaultBuggify || f.Node != "n1" || f.Site != site || f.Op < 1 || f.Op > 4000 {
			t.Fatalf("bad buggify fault %+v", f)
		}
	}
}

func TestBuggifyDisabledSiteNeverFires(t *testing.T) {
	w, inst := buggifyWorld(5)
	for i := range 50 {
		name := fmt.Sprintf("site %d", i)
		for _, f := range hits(w, inst, name, 1, 20) {
			if f && !w.bugs.sites[name].Enabled {
				t.Fatalf("disabled site %q fired", name)
			}
		}
	}
}

func TestBuggifyIsSuppressedByQuietPeriodsAndSetup(t *testing.T) {
	site := enabledSite(t, 9)
	w, inst := buggifyWorld(9)
	w.quietUntil = int64(time.Second)
	for _, f := range hits(w, inst, site, 1, 20) {
		if f {
			t.Fatal("a site fired during a quiet period")
		}
	}
	w.quietUntil, w.cfg.FaultsAfterSetup = 0, true
	for _, f := range hits(w, inst, site, 1, 20) {
		if f {
			t.Fatal("a site fired before setup completed")
		}
	}
	w.setupDone = true
	if !w.buggify(inst, site, 1) {
		t.Fatal("an enabled site with p=1 did not fire once faults were allowed")
	}
}

func TestBuggifyReplaysOnlyPlannedFirings(t *testing.T) {
	site := enabledSite(t, 11)
	w, inst := buggifyWorld(11)
	w.explicit = true
	w.faults = []Fault{{Kind: FaultBuggify, Node: "n1", Site: site, Op: 3}, {Kind: FaultBuggify, Node: "n2", Site: site, Op: 4}}
	got := hits(w, inst, site, 1, 6)
	if want := []bool{false, false, true, false, false, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit firings %v, want %v", got, want)
	}
	if !reflect.DeepEqual(w.ioFaults, []Fault{{Kind: FaultBuggify, Node: "n1", Site: site, Op: 3}}) {
		t.Fatalf("recorded %+v", w.ioFaults)
	}
}

func TestSwarmTogglesBuggify(t *testing.T) {
	spec := FaultSpec{Buggify: true, Swarm: true}
	on := 0
	for seed := range uint64(100) {
		if spec.forSeed(seed).Buggify {
			on++
		}
	}
	if on == 0 || on == 100 {
		t.Fatalf("swarm left buggify on in %d of 100 seeds", on)
	}
	if (FaultSpec{Swarm: true}).forSeed(1).Buggify {
		t.Fatal("swarm turned buggify on when it was off")
	}
}

func TestCatalogFindsBuggifySites(t *testing.T) {
	src := `package main

import (
	s "github.com/kunalgoesbyken/seedfall/sim"
	"github.com/antithesishq/antithesis-sdk-go/assert"
)

func f(name string) {
	if s.Buggify("drop the reply") {}
	if s.BuggifyWithProb("slow disk", 0.1) {}
	if s.Buggify(name) {}
	assert.Buggify("not a sim site")
	s.Reachable("restarted")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	props := 0
	for _, e := range fileProperties(fset, ".", file) {
		if e.Buggify {
			sites = append(sites, e.Message)
		} else {
			props++
		}
	}
	if !reflect.DeepEqual(sites, []string{"drop the reply", "slow disk"}) || props != 1 {
		t.Fatalf("cataloged sites %q and %d properties", sites, props)
	}
	cfg := defaultConfig()
	cfg.Faults.Buggify = true
	cov := newBuggifySites(&cfg, fileProperties(fset, ".", file))
	if cov["drop the reply"].Problem() != "never reached" || newCoverage(fileProperties(fset, ".", file))["drop the reply"] != nil {
		t.Fatal("a cataloged site is not reported as never reached, or leaked into property coverage")
	}
}
