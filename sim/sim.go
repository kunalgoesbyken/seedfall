package sim

import (
	"fmt"
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
)

func Always(cond bool, msg string, details ...any) { property(always, cond, msg, details) }

func AlwaysOrUnreachable(cond bool, msg string, details ...any) {
	property(alwaysOrUnreachable, cond, msg, details)
}

func Sometimes(cond bool, msg string, details ...any) { property(sometimes, cond, msg, details) }

func Reachable(msg string, details ...any) { property(reachable, true, msg, details) }

func Unreachable(msg string, details ...any) { property(unreachable, false, msg, details) }

func SetupComplete() { setupComplete() }

func QuietFor(d time.Duration) { quietFor(d) }

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

type Op struct{ id int32 }

func Put(client int, key, value string) Op { return call(client, "put", key, value) }

func Get(client int, key string) Op { return call(client, "get", key, "") }
