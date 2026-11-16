package gym

import (
	"slices"
	"testing"
)

func TestHiddenSeedsDependOnSaltAndTask(t *testing.T) {
	a := HiddenSeeds([]byte("one"), "task", 8)
	if !slices.Equal(a, HiddenSeeds([]byte("one"), "task", 8)) {
		t.Fatal("same inputs gave different seeds")
	}
	if slices.Equal(a, HiddenSeeds([]byte("two"), "task", 8)) {
		t.Fatal("salt does not change the seeds")
	}
	if slices.Equal(a, HiddenSeeds([]byte("one"), "other", 8)) {
		t.Fatal("task id does not change the seeds")
	}
	if !slices.Equal(a[:4], HiddenSeeds([]byte("one"), "task", 4)) {
		t.Fatal("a smaller n is not a prefix of a larger n")
	}
}

func TestSaltRequiresEnvironmentOrDev(t *testing.T) {
	t.Setenv("GYM_TEST_SALT", "")
	if _, err := Salt("GYM_TEST_SALT", false); err == nil {
		t.Fatal("an empty salt without dev was accepted")
	}
	if got, err := Salt("GYM_TEST_SALT", true); err != nil || string(got) != DevSalt {
		t.Fatalf("dev salt %q, %v", got, err)
	}
	t.Setenv("GYM_TEST_SALT", "secret")
	if got, _ := Salt("GYM_TEST_SALT", true); string(got) != "secret" {
		t.Fatalf("environment salt ignored, got %q", got)
	}
}

func TestResolveVariantsInheritAndMerge(t *testing.T) {
	seed := uint64(1)
	m := Manifest{
		Systems: map[string]System{"s": {}},
		Tasks: []Spec{{
			ID: "base", System: "s", VisibleSeed: &seed, N: 16,
			Config:  map[string]any{"faults": map[string]any{"a": 1, "b": 2}},
			Patches: Patches{Buggy: []string{"one.patch"}},
			Variants: []Spec{{
				ID:      "child",
				Config:  map[string]any{"faults": map[string]any{"b": 3}},
				Patches: Patches{Buggy: []string{"two.patch"}},
			}},
		}},
	}
	specs, err := m.Resolve()
	if err != nil || len(specs) != 2 {
		t.Fatalf("%d specs, %v", len(specs), err)
	}
	child := specs[1]
	faults := child.Config["faults"].(map[string]any)
	if child.System != "s" || child.N != 16 || faults["a"] != 1 || faults["b"] != 3 {
		t.Fatalf("child did not inherit and merge: %+v", child)
	}
	if !slices.Equal(child.Patches.Buggy, []string{"one.patch", "two.patch"}) {
		t.Fatalf("patches %v", child.Patches.Buggy)
	}
	if specs[0].Config["faults"].(map[string]any)["b"] != 2 {
		t.Fatal("variant merge mutated the parent")
	}
}

func splitFixture() []Spec {
	var specs []Spec
	for i, g := range []struct{ system, class string }{{"a", "x"}, {"a", "y"}, {"b", "x"}, {"b", "z"}, {"c", "x"}, {"c", "y"}, {"d", "w"}, {"e", "w"}} {
		for j := range 2 + i%2 {
			specs = append(specs, Spec{ID: g.system + "-" + g.class + "-" + string(rune('0'+j)), System: g.system, BugClass: g.class})
		}
	}
	return specs
}

func TestAssignSplitsKeepsGroupsWhole(t *testing.T) {
	specs := splitFixture()
	s := AssignSplits(specs, 0.25)
	if len(s.Train)+len(s.HeldOut) != len(specs) {
		t.Fatalf("%d train and %d held out of %d tasks", len(s.Train), len(s.HeldOut), len(specs))
	}
	split := map[string]string{}
	for _, id := range s.HeldOut {
		split[id] = SplitHeldOut
	}
	for _, id := range s.Train {
		if split[id] != "" {
			t.Fatalf("%s is in both splits", id)
		}
		split[id] = SplitTrain
	}
	for _, spec := range specs {
		if split[spec.ID] != s.Groups[GroupKey(spec)] {
			t.Fatalf("%s is %s but its group %q is %s", spec.ID, split[spec.ID], GroupKey(spec), s.Groups[GroupKey(spec)])
		}
	}
	if len(s.HeldOut) == 0 || len(s.HeldOut) > len(specs)/3 {
		t.Fatalf("%d held out of %d tasks", len(s.HeldOut), len(specs))
	}
}

func TestAssignSplitsIsDeterministicAndOrderFree(t *testing.T) {
	specs := splitFixture()
	a := AssignSplits(specs, 0.25)
	reversed := slices.Clone(specs)
	slices.Reverse(reversed)
	b := AssignSplits(reversed, 0.25)
	if !slices.Equal(a.Train, b.Train) || !slices.Equal(a.HeldOut, b.HeldOut) {
		t.Fatal("the split depends on manifest order")
	}
}
