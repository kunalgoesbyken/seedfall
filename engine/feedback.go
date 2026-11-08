package engine

import (
	"fmt"
	"maps"
	"math/bits"
	"slices"
	"strings"
	"time"

	"github.com/tetratelabs/wazero/api"
)

const growthSample = 4

var kindBits = func() map[string]uint32 {
	m := map[string]uint32{}
	for i, k := range FaultKinds {
		m[k] = 1 << i
	}
	return m
}()

type Signal struct {
	Key string
	At  time.Duration
}

type firing struct {
	msg    string
	cond   bool
	active uint32
}

type feedbackRun struct {
	firings   map[firing]int64
	functions map[string][]uint64
}

func (f *feedbackRun) property(w *world, msg string, cond bool) {
	if f == nil {
		return
	}
	key := firing{msg: msg, cond: cond, active: w.activeFaults()}
	if f.firings == nil {
		f.firings = map[firing]int64{}
	}
	if _, seen := f.firings[key]; !seen {
		f.firings[key] = w.now
	}
}

func (w *world) activeFaults() uint32 {
	var mask uint32
	for _, list := range [][]Fault{w.faults[:w.nextFault], w.ioFaults} {
		for _, f := range list {
			if int64(f.At) <= w.now && int64(f.Until) > w.now {
				mask |= kindBits[f.Kind]
			}
		}
	}
	return mask
}

func (f *feedbackRun) collect(n *node, upgraded bool, mod api.Module) {
	if f == nil {
		return
	}
	key := n.name()
	if upgraded {
		key += "@upgrade"
	}
	for word := 0; ; word++ {
		g := mod.ExportedGlobal(fmt.Sprintf("%s%d", coverExport, word))
		if g == nil {
			return
		}
		if f.functions == nil {
			f.functions = map[string][]uint64{}
		}
		if word == len(f.functions[key]) {
			f.functions[key] = append(f.functions[key], 0)
		}
		f.functions[key][word] |= g.Get()
	}
}

func activeNames(mask uint32) string {
	var names []string
	for k, bit := range kindBits {
		if mask&bit != 0 {
			names = append(names, k)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ",")
}

func (f *feedbackRun) signals(res Result) []Signal {
	if f == nil {
		return nil
	}
	firsts := map[string]time.Duration{}
	note := func(key string, at time.Duration) {
		if first, ok := firsts[key]; !ok || at < first {
			firsts[key] = at
		}
	}
	for k, at := range f.firings {
		key := fmt.Sprintf("property %q = %t", k.msg, k.cond)
		if names := activeNames(k.active); names != "" {
			key += " during " + names
		}
		note(key, time.Duration(at))
	}
	for _, fault := range res.FaultsApplied() {
		note(strings.TrimRight(fmt.Sprintf("fault %s %s %s", fault.Kind, fault.Node, fault.Site), " "), time.Duration(fault.At))
	}
	out := make([]Signal, 0, len(firsts))
	for _, key := range slices.Sorted(maps.Keys(firsts)) {
		out = append(out, Signal{Key: key, At: firsts[key]})
	}
	return out
}

type feedback struct {
	hits      map[string]int
	functions map[string][]uint64
	reached   int
}

func newFeedback() *feedback {
	return &feedback{hits: map[string]int{}, functions: map[string][]uint64{}}
}

func (f *feedback) absorb(res Result) (fresh []string, functions int) {
	for _, s := range res.Signals {
		if f.hits[s.Key] == 0 {
			fresh = append(fresh, s.Key)
		}
		f.hits[s.Key]++
	}
	for _, node := range slices.Sorted(maps.Keys(res.Functions)) {
		bitmap := res.Functions[node]
		seen := f.functions[node]
		if len(seen) < len(bitmap) {
			seen = append(seen, make([]uint64, len(bitmap)-len(seen))...)
			f.functions[node] = seen
		}
		for i, word := range bitmap {
			if diff := word &^ seen[i]; diff != 0 {
				functions += bits.OnesCount64(diff)
				seen[i] |= word
			}
		}
	}
	f.reached += functions
	return fresh, functions
}

type GrowthPoint struct {
	Round        int           `json:"round"`
	Runs         int           `json:"runs"`
	NewSignals   int           `json:"new_signals"`
	Signals      int           `json:"signals"`
	NewFunctions int           `json:"new_functions"`
	Functions    int           `json:"functions"`
	Corpus       int           `json:"corpus,omitempty"`
	Failures     int           `json:"failures"`
	Elapsed      time.Duration `json:"elapsed_ns"`
	Sample       []string      `json:"new_signal_sample,omitempty"`
}

func (p GrowthPoint) Novel() bool { return p.NewSignals+p.NewFunctions > 0 }

func (f *feedback) point(round, runs, corpus, failures int, fresh []string, functions int, took time.Duration) GrowthPoint {
	return GrowthPoint{
		Round: round, Runs: runs, NewSignals: len(fresh), Signals: len(f.hits), NewFunctions: functions, Functions: f.reached,
		Corpus: corpus, Failures: failures, Elapsed: took, Sample: fresh[:min(len(fresh), growthSample)],
	}
}

func quietRounds(p GrowthPoint, quiet int) int {
	if p.Novel() {
		return 0
	}
	return quiet + 1
}
