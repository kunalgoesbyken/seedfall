package engine

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"time"
)

const (
	CorpusDir        = "corpus"
	exploreStream    = 0x5eed5eed
	idLength         = 12
	shiftOpsMax      = 3
	checkpointWeight = 3
	mutationKinds    = 3
	shiftTimeDivisor = 20
)

type ExploreOptions struct {
	Lo, Hi       uint64
	Rounds       int
	Batch        int
	UntilNovelty int
	Budget       time.Duration
	Jobs         int
	Seed         uint64
	KeepGoing    bool
	Corpus       string
	OnResult     func(Result)
	OnRound      func(GrowthPoint)
}

type CorpusEntry struct {
	Seed     uint64
	Schedule Schedule
	Signals  []Signal
	Horizon  time.Duration
}

type Exploration struct {
	Summary
	Corpus int
	Loaded int
	Stale  int
	Dir    string
}

type exploreTask struct {
	seed  uint64
	sched Schedule
}

type explorer struct {
	r      *Runner
	opts   ExploreOptions
	rng    *rand.Rand
	fb     *feedback
	corpus []*CorpusEntry
	seen   map[string]bool
	sum    Summary
	dir    string
}

func Explore(r *Runner, opts ExploreOptions) (Exploration, error) {
	started := time.Now()
	e := &explorer{r: r, opts: opts, rng: rand.New(rand.NewPCG(opts.Seed, exploreStream)), fb: newFeedback(), seen: map[string]bool{}, sum: newSummary(r), dir: cmp.Or(opts.Corpus, DefaultCorpusDir(r.cfg))}
	out := Exploration{Dir: e.dir}
	loaded, stale, err := loadCorpus(e.dir, r.cfg)
	if err != nil {
		return out, err
	}
	out.Loaded, out.Stale = len(loaded), stale
	tasks := append(loaded, baselineTasks(opts.Lo, opts.Hi)...)
	for round, quiet := 1, 0; ; round++ {
		point, err := e.round(round, tasks, started)
		if err != nil {
			return out, err
		}
		quiet = quietRounds(point, quiet)
		if stop := e.stopReason(round, quiet, started); stop != "" {
			e.sum.Stopped = stop
			break
		}
		tasks = e.children()
	}
	e.sum.Elapsed = time.Since(started)
	out.Summary, out.Corpus = e.sum, len(e.corpus)
	return out, nil
}

func baselineTasks(lo, hi uint64) []exploreTask {
	var tasks []exploreTask
	for seed := lo; seed <= hi; seed++ {
		tasks = append(tasks, exploreTask{seed: seed})
	}
	return tasks
}

func (e *explorer) stopReason(round, quiet int, started time.Time) string {
	switch {
	case !e.opts.KeepGoing && e.sum.First != nil:
		return "failure found"
	case len(e.corpus) == 0:
		return "the corpus is empty, nothing to mutate"
	case e.opts.Rounds > 0 && round >= e.opts.Rounds:
		return fmt.Sprintf("%d rounds done", e.opts.Rounds)
	case e.opts.Budget > 0 && time.Since(started) >= e.opts.Budget:
		return fmt.Sprintf("time budget %s spent", e.opts.Budget)
	case e.opts.UntilNovelty > 0 && quiet >= e.opts.UntilNovelty:
		return fmt.Sprintf("%d rounds in a row added nothing new", quiet)
	}
	return ""
}

func (e *explorer) round(round int, tasks []exploreTask, started time.Time) (GrowthPoint, error) {
	results := make([]Result, len(tasks))
	forEach(len(tasks), e.opts.Jobs, func(i int) { results[i] = e.r.Replay(tasks[i].seed, tasks[i].sched) })
	var fresh []string
	functions := 0
	for i, res := range results {
		e.sum.record(res)
		if e.opts.OnResult != nil {
			e.opts.OnResult(res)
		}
		sigs, funcs := e.fb.absorb(res)
		fresh, functions = append(fresh, sigs...), functions+funcs
		if (len(sigs) > 0 || funcs > 0) && !res.Failed {
			if err := e.keep(tasks[i], res); err != nil {
				return GrowthPoint{}, err
			}
		}
	}
	point := e.fb.point(round, e.sum.Runs(), len(e.corpus), len(e.sum.Failures), fresh, functions, time.Since(started))
	e.sum.Growth = append(e.sum.Growth, point)
	if e.opts.OnRound != nil {
		e.opts.OnRound(point)
	}
	return point, nil
}

func (e *explorer) keep(t exploreTask, res Result) error {
	entry := &CorpusEntry{Seed: res.Seed, Schedule: res.Schedule(t.sched), Signals: res.Signals, Horizon: res.Horizon}
	id := ScheduleID(entry.Seed, entry.Schedule)
	if e.seen[id] {
		return nil
	}
	e.seen[id] = true
	e.corpus = append(e.corpus, entry)
	return WriteScheduleFile(filepath.Join(e.dir, fmt.Sprintf("%d-%s.json", entry.Seed, id)), ScheduleFile{Seed: entry.Seed, Schedule: entry.Schedule})
}

func ScheduleID(seed uint64, s Schedule) string {
	b, _ := json.Marshal(ScheduleFile{Seed: seed, Schedule: s})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:idLength]
}

func (e *explorer) children() []exploreTask {
	tasks := make([]exploreTask, max(e.opts.Batch, 1))
	for i := range tasks {
		tasks[i] = e.child(e.pick())
	}
	return tasks
}

func rarity(hits int) float64 { return 1 / float64(max(hits, 1)) }

func (e *explorer) weight(entry *CorpusEntry) float64 {
	w := 0.0
	for _, s := range entry.Signals {
		w += rarity(e.fb.hits[s.Key])
	}
	return w
}

func (e *explorer) pick() *CorpusEntry {
	total := 0.0
	for _, entry := range e.corpus {
		total += e.weight(entry)
	}
	if total <= 0 {
		return e.corpus[e.rng.IntN(len(e.corpus))]
	}
	at := e.rng.Float64() * total
	for _, entry := range e.corpus {
		if at -= e.weight(entry); at <= 0 {
			return entry
		}
	}
	return e.corpus[len(e.corpus)-1]
}

func (e *explorer) pickSignal(signals []Signal) Signal {
	total := 0.0
	for _, s := range signals {
		total += rarity(e.fb.hits[s.Key])
	}
	at := e.rng.Float64() * total
	for _, s := range signals {
		if at -= rarity(e.fb.hits[s.Key]); at <= 0 {
			return s
		}
	}
	return signals[len(signals)-1]
}

func (e *explorer) child(parent *CorpusEntry) exploreTask {
	s := parent.Schedule
	s.Branches = slices.Clone(s.Branches)
	last := Duration(-1)
	if n := len(s.Branches); n > 0 {
		last = s.Branches[n-1].At
	}
	checkpoints := slices.DeleteFunc(slices.Clone(parent.Signals), func(sig Signal) bool { return Duration(sig.At) <= last })
	switch choice := e.rng.IntN(checkpointWeight + 2); {
	case choice < checkpointWeight && len(checkpoints) > 0:
		sig := e.pickSignal(checkpoints)
		s.Branches = append(s.Branches, Branch{At: Duration(sig.At) + 1, Key: e.rng.Uint64() | 1, reroll: true})
	case choice == checkpointWeight && parent.Horizon > time.Duration(last+1):
		at := last + 1 + Duration(e.rng.Int64N(int64(parent.Horizon-time.Duration(last+1))))
		s.Branches = append(s.Branches, Branch{At: at, Key: e.rng.Uint64() | 1, Stream: BranchStreams[e.rng.IntN(len(BranchStreams))]})
	default:
		e.mutateFaults(&s, parent)
	}
	return exploreTask{seed: parent.Seed, sched: s}
}

func (e *explorer) mutateFaults(s *Schedule, parent *CorpusEntry) {
	faults := slices.Clone(s.Faults)
	switch e.rng.IntN(mutationKinds) {
	case 0:
		pool := slices.DeleteFunc(generateFaults(e.r.cfg, e.r.cfg.Faults.forSeed(parent.Seed), rand.New(rand.NewPCG(e.rng.Uint64(), exploreStream))), func(f Fault) bool { return observedAtRuntime(f.Kind) })
		if len(pool) > 0 {
			faults = append(faults, pool[e.rng.IntN(len(pool))])
		}
	case 1:
		if len(faults) > 0 {
			i := e.rng.IntN(len(faults))
			faults = slices.Delete(faults, i, i+1)
		}
	default:
		if len(faults) > 0 {
			shiftFault(&faults[e.rng.IntN(len(faults))], e.rng, parent.Horizon)
		}
	}
	slices.SortStableFunc(faults, func(a, b Fault) int { return cmp.Compare(a.At, b.At) })
	s.Faults = faults
}

func shiftFault(f *Fault, rng *rand.Rand, horizon time.Duration) {
	if f.FiresAtOp() {
		f.Op = max(f.Op+rng.Int64N(2*shiftOpsMax+1)-shiftOpsMax, 1)
		return
	}
	limit := int64(horizon) / shiftTimeDivisor
	if limit <= 0 {
		return
	}
	by := Duration(rng.Int64N(2*limit+1) - limit)
	span := f.Until - f.At
	f.At = max(f.At+by, 0)
	if span > 0 {
		f.Until = f.At + span
	}
}

func DefaultCorpusDir(cfg *Config) string {
	return filepath.Join(cfg.Dir, ".seedfall", CorpusDir, cfg.Stem())
}

func loadCorpus(dir string, cfg *Config) ([]exploreTask, int, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, 0, err
	}
	slices.Sort(paths)
	var tasks []exploreTask
	stale := 0
	for _, p := range paths {
		file, err := ReadScheduleFile(p)
		if err != nil {
			return nil, 0, err
		}
		if cfg.CheckSchedule(file.Schedule) != nil {
			stale++
			continue
		}
		tasks = append(tasks, exploreTask{seed: file.Seed, sched: file.Schedule})
	}
	return tasks, stale, nil
}
