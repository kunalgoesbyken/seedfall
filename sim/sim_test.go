package sim

import (
	"testing"
	"time"
)

func TestBuggifyIsInertOutsideTheSimulator(t *testing.T) {
	for range 100 {
		if Buggify("site") || BuggifyWithProb("site", 1) {
			t.Fatal("a buggify site fired outside the simulator")
		}
	}
}

func TestRandomChoiceCoversEveryItem(t *testing.T) {
	if RandomChoice([]int(nil)) != 0 {
		t.Fatal("an empty choice is not the zero value")
	}
	items := []string{"a", "b", "c"}
	seen := map[string]bool{}
	for range 300 {
		seen[RandomChoice(items)] = true
	}
	if len(seen) != len(items) {
		t.Fatalf("300 choices saw only %v", seen)
	}
}

func TestEventuallyOutsideTheSimulator(t *testing.T) {
	polls := 0
	if !Eventually(func() bool { polls++; return polls == 3 }, "third poll succeeds", time.Second) || polls != 3 {
		t.Fatalf("Eventually stopped after %d polls", polls)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an Eventually that is never true did not panic outside the simulator")
		}
	}()
	Eventually(func() bool { return false }, "never", 10*time.Millisecond)
}

func TestBudgetIsTheFullCountOutsideTheSimulator(t *testing.T) {
	for _, n := range []int{0, 1, 200} {
		if got := Budget(n); got != n {
			t.Fatalf("Budget(%d) = %d outside the simulator", n, got)
		}
	}
}
