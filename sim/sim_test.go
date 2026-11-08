package sim

import "testing"

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
