package sim

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"os"
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

func Always(cond bool, msg string, details ...any) { property(always, cond, msg, details) }

func AlwaysOrUnreachable(cond bool, msg string, details ...any) {
	property(alwaysOrUnreachable, cond, msg, details)
}

func Sometimes(cond bool, msg string, details ...any) { property(sometimes, cond, msg, details) }

func Reachable(msg string, details ...any) { property(reachable, true, msg, details) }

func Unreachable(msg string, details ...any) { property(unreachable, false, msg, details) }

func Eventually(cond func() bool, msg string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for !cond() {
		if !time.Now().Before(deadline) {
			property(eventually, false, msg, []any{"not true within ", within})
			return false
		}
		time.Sleep(within / eventuallyPolls)
	}
	property(eventually, true, msg, nil)
	return true
}

func SetupComplete() { setupComplete() }

func QuietFor(d time.Duration) { quietFor(d) }

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

func Put(client int, key, value string) Op { return call(client, "put", key, value) }

func Get(client int, key string) Op { return call(client, "get", key, "") }
