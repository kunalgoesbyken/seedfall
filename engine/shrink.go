package engine

import "slices"

func Shrink(r *Runner, failing Result, progress func(int)) Result {
	best := failing
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(best.Faults); i++ {
			candidate := slices.Delete(slices.Clone(best.Faults), i, i+1)
			res := r.Run(best.Seed, candidate)
			if !res.sameFailure(failing) {
				continue
			}
			best, changed = res, true
			progress(len(best.Faults))
			i--
		}
	}
	return best
}
