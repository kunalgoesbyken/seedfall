package engine

import "strings"

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
