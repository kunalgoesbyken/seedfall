package engine

import (
	"math"
	"testing"
)

func TestRotateSeedsMovesByWholeWidths(t *testing.T) {
	for _, c := range []struct{ lo, hi, rotation, wantLo, wantHi uint64 }{
		{1, 200, 0, 1, 200},
		{1, 200, 1, 201, 400},
		{1, 200, 6, 1201, 1400},
		{7, 7, 3, 10, 10},
		{math.MaxUint64 - 9, math.MaxUint64 - 5, 1, math.MaxUint64 - 4, math.MaxUint64},
	} {
		lo, hi, err := RotateSeeds(c.lo, c.hi, c.rotation)
		if err != nil || lo != c.wantLo || hi != c.wantHi {
			t.Errorf("RotateSeeds(%d, %d, %d) = %d, %d, %v", c.lo, c.hi, c.rotation, lo, hi, err)
		}
	}
}

func TestRotateSeedsRefusesToWrap(t *testing.T) {
	for _, c := range []struct{ lo, hi, rotation uint64 }{
		{math.MaxUint64 - 9, math.MaxUint64 - 5, 2},
		{1, math.MaxUint64 / 2, 3},
	} {
		if lo, hi, err := RotateSeeds(c.lo, c.hi, c.rotation); err == nil {
			t.Errorf("RotateSeeds(%d, %d, %d) = %d, %d, want an error", c.lo, c.hi, c.rotation, lo, hi)
		}
	}
}
