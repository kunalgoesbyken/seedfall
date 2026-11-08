package main

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

const (
	steps     = 2000
	depth     = 6
	hitBelow  = 26
	stepSleep = time.Millisecond
)

var levels = [depth]string{"level 1 reached", "level 2 reached", "level 3 reached", "level 4 reached", "level 5 reached", "level 6 reached"}

func draw() byte {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return b[0]
}

func main() {
	level := 0
	for range steps {
		time.Sleep(stepSleep)
		if draw() >= hitBelow {
			level = 0
			continue
		}
		level++
		sim.AlwaysOrUnreachable(true, levels[level-1])
		sim.Assert(level < depth, fmt.Sprintf("the vault opened after %d consecutive lucky draws", level))
	}
}
