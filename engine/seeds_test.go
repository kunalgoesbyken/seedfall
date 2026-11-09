package engine

import (
	"math"
	"strings"
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

func TestParseSeeds(t *testing.T) {
	for _, c := range []struct {
		in     string
		lo, hi uint64
	}{
		{"7", 7, 7},
		{"1-200", 1, 200},
		{"0-18446744073709551614", 0, math.MaxUint64 - 1},
	} {
		if lo, hi, err := ParseSeeds(c.in); err != nil || lo != c.lo || hi != c.hi {
			t.Errorf("ParseSeeds(%q) = %d, %d, %v", c.in, lo, hi, err)
		}
	}
	for _, in := range []string{"", "x", "5-3", "1-x", "1-18446744073709551615", "0-18446744073709551615", "18446744073709551615"} {
		if lo, hi, err := ParseSeeds(in); err == nil {
			t.Errorf("ParseSeeds(%q) = %d, %d, want an error", in, lo, hi)
		}
	}
	if _, _, err := ParseSeeds("1-18446744073709551615"); !strings.Contains(err.Error(), "wrap around") {
		t.Errorf("the error does not say why: %v", err)
	}
}
