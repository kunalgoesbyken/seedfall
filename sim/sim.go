package sim

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"time"
)

func Enabled() bool { return os.Getenv("SEEDFALL") == "1" }

func Node() string {
	if n := os.Getenv("SEEDFALL_NODE"); n != "" {
		return n
	}
	h, _ := os.Hostname()
	return h
}

type kind int32

const (
	always kind = iota
	sometimes
	reachable
	unreachable
	alwaysOrUnreachable
	eventually
)

const eventuallyPolls = 100

func Always(cond bool, msg string, details ...any) { property(always, cond, msg, describe(details)) }

func AlwaysOrUnreachable(cond bool, msg string, details ...any) {
	property(alwaysOrUnreachable, cond, msg, describe(details))
}

func Sometimes(cond bool, msg string, details ...any) {
	property(sometimes, cond, msg, describe(details))
}

func Reachable(msg string, details ...any) { property(reachable, true, msg, describe(details)) }

func Unreachable(msg string, details ...any) { property(unreachable, false, msg, describe(details)) }

func Eventually(cond func() bool, msg string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for !cond() {
		if !time.Now().Before(deadline) {
			property(eventually, false, msg, describe([]any{"not true within ", within}))
			return false
		}
		time.Sleep(within / eventuallyPolls)
	}
	property(eventually, true, msg, "")
	return true
}

type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr | ~float32 | ~float64
}

func AlwaysGreaterThan[T Number](left, right T, msg string, details ...any) {
	compare(always, left > right, float64(left)-float64(right), left, right, msg, details)
}

func AlwaysGreaterThanOrEqualTo[T Number](left, right T, msg string, details ...any) {
	compare(always, left >= right, float64(left)-float64(right), left, right, msg, details)
}

func AlwaysLessThan[T Number](left, right T, msg string, details ...any) {
	compare(always, left < right, float64(right)-float64(left), left, right, msg, details)
}

func AlwaysLessThanOrEqualTo[T Number](left, right T, msg string, details ...any) {
	compare(always, left <= right, float64(right)-float64(left), left, right, msg, details)
}

func SometimesGreaterThan[T Number](left, right T, msg string, details ...any) {
	compare(sometimes, left > right, float64(left)-float64(right), left, right, msg, details)
}

func SometimesGreaterThanOrEqualTo[T Number](left, right T, msg string, details ...any) {
	compare(sometimes, left >= right, float64(left)-float64(right), left, right, msg, details)
}

func SometimesLessThan[T Number](left, right T, msg string, details ...any) {
	compare(sometimes, left < right, float64(right)-float64(left), left, right, msg, details)
}

func SometimesLessThanOrEqualTo[T Number](left, right T, msg string, details ...any) {
	compare(sometimes, left <= right, float64(right)-float64(left), left, right, msg, details)
}

func compare[T Number](k kind, cond bool, margin float64, left, right T, msg string, details []any) {
	numeric(k, cond, margin, msg, detailJSON(map[string]any{"left": left, "right": right}, details))
}

func AlwaysSome(named map[string]bool, msg string, details ...any) {
	property(always, slices.Contains(slices.Collect(maps.Values(named)), true), msg, grouped(named, details))
}

func SometimesAll(named map[string]bool, msg string, details ...any) {
	property(sometimes, !slices.Contains(slices.Collect(maps.Values(named)), false), msg, grouped(named, details))
}

func grouped(named map[string]bool, details []any) string {
	fields := map[string]any{}
	for name, v := range named {
		fields[name] = v
	}
	return detailJSON(fields, details)
}

func detailJSON(fields map[string]any, details []any) string {
	if len(details) > 0 {
		fields["details"] = describe(details)
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return fmt.Sprint(fields)
	}
	return string(b)
}

func Event(name string, details any) {
	b, err := json.Marshal(details)
	if err != nil {
		b, _ = json.Marshal(fmt.Sprint(details))
	}
	event(name, string(b))
}

func SetupComplete() { setupComplete() }

func QuietFor(d time.Duration) { quietFor(d) }

func Budget(n int) int { return budget(n) }

const BuggifyProb = 0.25

func Buggify(site string) bool { return buggify(site, BuggifyProb) }

func BuggifyWithProb(site string, p float64) bool { return buggify(site, p) }

func Random() uint64 {
	var b [8]byte
	rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

func RandomChoice[T any](items []T) T {
	var zero T
	if len(items) == 0 {
		return zero
	}
	n := uint64(len(items))
	ceiling := math.MaxUint64 / n * n
	v := Random()
	for v >= ceiling {
		v = Random()
	}
	return items[v%n]
}

func Assert(cond bool, msg string) {
	if !cond {
		Fail(msg)
	}
}

func describe(details []any) string {
	if len(details) == 0 {
		return ""
	}
	return fmt.Sprint(details...)
}

func Put(client int, key, value string) Op { return Invoke(client, "put", key, value) }

func Get(client int, key string) Op { return Invoke(client, "get", key, nil) }

func Invoke(client int, f, key string, value any) Op { return call(client, f, key, value) }

type Mop struct {
	F     string
	Key   string
	Value any
}

func (m Mop) MarshalJSON() ([]byte, error) { return json.Marshal([]any{m.F, m.Key, m.Value}) }

func Append(key, value string) Mop { return Mop{F: "append", Key: key, Value: value} }

func Read(key string) Mop { return Mop{F: "r", Key: key} }

func Txn(client int, mops ...Mop) Op { return call(client, "txn", "", mops) }
