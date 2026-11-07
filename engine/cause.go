package engine

import (
	"errors"
	"slices"
	"sync"
	"time"
)

type CausePoint struct {
	At     time.Duration `json:"at"`
	Runs   int           `json:"runs"`
	Failed int           `json:"failed"`
	Faults []Fault       `json:"faults_since_previous"`
}

func (p CausePoint) Probability() float64 { return float64(p.Failed) / float64(p.Runs) }

type CauseOptions struct {
	Points   int
	Branches int
	Jobs     int
}

func Cause(r *Runner, seed uint64, opts CauseOptions) (Result, []CausePoint, error) {
	original := r.Run(seed, nil)
	if !original.Failed {
		return original, nil, errors.New("seed does not fail, nothing to explain")
	}
	points := causeTimes(original, opts.Points)
	runs := make([][]bool, len(points))
	for i := range runs {
		runs[i] = make([]bool, opts.Branches)
	}
	forEach(len(points)*opts.Branches, opts.Jobs, func(task int) {
		p, b := task/opts.Branches, task%opts.Branches
		runs[p][b] = r.Branch(seed, points[p].At, uint64(b+1)).Failed
	})
	for i := range points {
		points[i].Runs = opts.Branches
		for _, failed := range runs[i] {
			if failed {
				points[i].Failed++
			}
		}
	}
	return original, points, nil
}

func causeTimes(res Result, n int) []CausePoint {
	var times []time.Duration
	for i := range max(n, 1) {
		times = append(times, res.At*time.Duration(i)/time.Duration(max(n, 1)))
	}
	faults := res.FaultsBefore()
	for _, f := range faults {
		times = append(times, time.Duration(f.At), time.Duration(f.At)+1)
	}
	slices.Sort(times)
	times = slices.Compact(times)
	points := make([]CausePoint, len(times))
	for i, t := range times {
		points[i].At = t
		for _, f := range faults {
			if time.Duration(f.At) < t && (i == 0 || time.Duration(f.At) >= times[i-1]) {
				points[i].Faults = append(points[i].Faults, f)
			}
		}
	}
	return points
}

func forEach(n, jobs int, fn func(int)) {
	tasks := make(chan int)
	var wg sync.WaitGroup
	for range max(jobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range tasks {
				fn(i)
			}
		}()
	}
	for i := range n {
		tasks <- i
	}
	close(tasks)
	wg.Wait()
}
