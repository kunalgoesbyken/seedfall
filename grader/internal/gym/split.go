package gym

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"slices"
	"sort"
)

const (
	SplitTrain   = "train"
	SplitHeldOut = "held_out"
	splitSalt    = "seedfall-gym-split-v1"
)

type Splits struct {
	HeldOutFraction float64           `json:"held_out_fraction"`
	Train           []string          `json:"train"`
	HeldOut         []string          `json:"held_out"`
	Groups          map[string]string `json:"groups"`
}

func GroupKey(s Spec) string { return s.System + " / " + s.BugClass }

func AssignSplits(specs []Spec, heldOutFraction float64) Splits {
	members := map[string][]string{}
	for _, s := range specs {
		members[GroupKey(s)] = append(members[GroupKey(s)], s.ID)
	}
	order := make([]string, 0, len(members))
	for key := range members {
		order = append(order, key)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := groupHash(order[i]), groupHash(order[j])
		if a != b {
			return a < b
		}
		return order[i] < order[j]
	})
	target := int(math.Round(heldOutFraction * float64(len(specs))))
	out := Splits{HeldOutFraction: heldOutFraction, Groups: map[string]string{}}
	held := 0
	for _, key := range order {
		if held+len(members[key]) <= target {
			held += len(members[key])
			out.Groups[key] = SplitHeldOut
			continue
		}
		out.Groups[key] = SplitTrain
	}
	for _, s := range specs {
		if out.Groups[GroupKey(s)] == SplitHeldOut {
			out.HeldOut = append(out.HeldOut, s.ID)
		} else {
			out.Train = append(out.Train, s.ID)
		}
	}
	slices.Sort(out.Train)
	slices.Sort(out.HeldOut)
	return out
}

func groupHash(key string) string {
	sum := sha256.Sum256([]byte(splitSalt + "|" + key))
	return hex.EncodeToString(sum[:])
}
