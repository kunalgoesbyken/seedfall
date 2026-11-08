package kvlog

import (
	"testing"

	"github.com/kunalgoesbyken/seedfall/seedfalltest"
)

func TestDurabilityUnderCrashes(t *testing.T) {
	seedfalltest.Sweep(t, "seedfall.fixed.json", "1-100")
}

func TestPinnedRegressions(t *testing.T) {
	seedfalltest.Pinned(t, "seedfall.fixed.json")
}

func TestTornTailStaysFixed(t *testing.T) {
	seedfalltest.Replay(t, "seedfall.fixed.json", "torntail.faults.json")
}

func TestKnownSeeds(t *testing.T) {
	seedfalltest.Seeds(t, "seedfall.fixed.json", []uint64{1, 9})
}
