package engine

import (
	"strings"
	"unicode"
)

type FailureClass string

const (
	FailProperty        FailureClass = "property"
	FailAssert          FailureClass = "assert"
	FailCrash           FailureClass = "crash"
	FailExit            FailureClass = "exit"
	FailWatchdog        FailureClass = "watchdog"
	FailDeadlock        FailureClass = "deadlock"
	FailLinearizability FailureClass = "linearizability"
	FailNondeterminism  FailureClass = "nondeterminism"
	FailSetup           FailureClass = "setup"
	FailLiveness        FailureClass = "liveness"
	FailIsolation       FailureClass = "isolation"
)

type Failure struct {
	Class    FailureClass `json:"class"`
	Node     string       `json:"node,omitempty"`
	Property string       `json:"property,omitempty"`
}

func (f Failure) Key() string {
	parts := []string{string(f.Class)}
	for _, p := range []string{f.Node, f.Property} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

func (r Result) sameFailure(o Result) bool {
	return r.Failed && o.Failed && r.Failure.Key() == o.Failure.Key() && maskDigits(r.Reason) == maskDigits(o.Reason)
}

func maskDigits(s string) string {
	var b strings.Builder
	digit := false
	for _, c := range s {
		if unicode.IsDigit(c) {
			if !digit {
				b.WriteByte('#')
			}
			digit = true
			continue
		}
		digit = false
		b.WriteRune(c)
	}
	return b.String()
}
