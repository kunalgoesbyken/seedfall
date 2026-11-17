package gym

import (
	"fmt"
	"maps"
	"slices"
)

type System struct {
	Trees      map[string]string `json:"trees"`
	Editable   []string          `json:"editable"`
	Config     map[string]any    `json:"config"`
	ModuleFrom string            `json:"module_from,omitempty"`
	Exploits   []string          `json:"exploits,omitempty"`
}

type Patches struct {
	Buggy []string `json:"buggy,omitempty"`
	Fixed []string `json:"fixed,omitempty"`
	Cheat []string `json:"cheat,omitempty"`
}

type Spec struct {
	ID          string         `json:"id"`
	Extends     string         `json:"extends,omitempty"`
	System      string         `json:"system,omitempty"`
	BugClass    string         `json:"bug_class,omitempty"`
	Difficulty  string         `json:"difficulty,omitempty"`
	N           int            `json:"n,omitempty"`
	Symptom     string         `json:"symptom,omitempty"`
	VisibleSeed *uint64        `json:"visible_seed,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
	Patches     Patches        `json:"patches,omitempty"`
	Variants    []Spec         `json:"variants,omitempty"`
}

type Manifest struct {
	SeedfallCommit string            `json:"seedfall_commit"`
	N              int               `json:"n"`
	Systems        map[string]System `json:"systems"`
	Tasks          []Spec            `json:"tasks"`
}

func (m Manifest) Resolve() ([]Spec, error) {
	var flat []Spec
	var walk func(parent *Spec, s Spec) error
	walk = func(parent *Spec, s Spec) error {
		if s.Extends != "" && parent == nil {
			i := slices.IndexFunc(flat, func(f Spec) bool { return f.ID == s.Extends })
			if i < 0 {
				return fmt.Errorf("task %s extends unknown task %s", s.ID, s.Extends)
			}
			parent = &flat[i]
		}
		if parent != nil {
			s = inherit(*parent, s)
		}
		if slices.ContainsFunc(flat, func(f Spec) bool { return f.ID == s.ID }) {
			return fmt.Errorf("duplicate task id %s", s.ID)
		}
		variants := s.Variants
		s.Variants = nil
		flat = append(flat, s)
		for _, v := range variants {
			parentCopy := s
			v.Extends = ""
			if err := walk(&parentCopy, v); err != nil {
				return err
			}
		}
		return nil
	}
	for _, t := range m.Tasks {
		if err := walk(nil, t); err != nil {
			return nil, err
		}
	}
	for _, s := range flat {
		if _, ok := m.Systems[s.System]; !ok {
			return nil, fmt.Errorf("task %s uses unknown system %q", s.ID, s.System)
		}
		if s.VisibleSeed == nil {
			return nil, fmt.Errorf("task %s has no visible_seed", s.ID)
		}
	}
	return flat, nil
}

func inherit(parent, child Spec) Spec {
	out := child
	out.Extends = ""
	if out.System == "" {
		out.System = parent.System
	}
	if out.BugClass == "" {
		out.BugClass = parent.BugClass
	}
	if out.Difficulty == "" {
		out.Difficulty = parent.Difficulty
	}
	if out.N == 0 {
		out.N = parent.N
	}
	if out.Symptom == "" {
		out.Symptom = parent.Symptom
	}
	if out.VisibleSeed == nil {
		out.VisibleSeed = parent.VisibleSeed
	}
	out.Config = MergeConfig(parent.Config, child.Config)
	out.Patches = Patches{
		Buggy: slices.Concat(parent.Patches.Buggy, child.Patches.Buggy),
		Fixed: slices.Concat(parent.Patches.Fixed, child.Patches.Fixed),
		Cheat: slices.Concat(parent.Patches.Cheat, child.Patches.Cheat),
	}
	return out
}

func MergeConfig(base, patch map[string]any) map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}
		pm, pok := v.(map[string]any)
		bm, bok := out[k].(map[string]any)
		if pok && bok {
			out[k] = MergeConfig(bm, pm)
			continue
		}
		out[k] = v
	}
	return out
}
