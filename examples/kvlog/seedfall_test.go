package kvlog

import (
	"testing"

	"github.com/kunalgoesbyken/seedfall/seedfalltest"
)

func TestDurabilityUnderCrashes(t *testing.T) {
	seedfalltest.Sweep(t, "seedfall.fixed.json", "1-100")
}
