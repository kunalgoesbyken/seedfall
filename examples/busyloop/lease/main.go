package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

func checksum(data []byte) uint32 {
	h := uint32(2166136261)
	for _, b := range data {
		h = (h ^ uint32(b)) * 16777619
	}
	return h
}

func main() {
	writes := flag.Int("writes", 10, "writes the leader commits")
	size := flag.Int("size", 4<<20, "bytes checksummed before each write")
	lease := flag.Duration("lease", 30*time.Millisecond, "how long a lease lasts once acquired")
	recheck := flag.Bool("recheck", false, "check the lease again after the checksum, just before committing")
	spin := flag.Bool("spin", false, "wait for the replicator by spinning on a flag instead of a channel")
	flag.Parse()

	ready := false
	connected := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		ready = true
		close(connected)
	}()
	if *spin {
		for !ready {
		}
	} else {
		<-connected
	}

	data := make([]byte, *size)
	var until time.Time
	for w := 0; w < *writes; {
		if now := time.Now(); !now.Before(until) {
			until = now.Add(*lease)
		}
		sum := checksum(data)
		commit := time.Now()
		if *recheck && !commit.Before(until) {
			continue
		}
		sim.Always(commit.Before(until), "a write commits while the lease is held", map[string]any{"write": w, "late": commit.Sub(until).String()})
		data[w] = byte(sum)
		w++
		time.Sleep(time.Duration(1+rand.IntN(20)) * time.Millisecond)
	}
	fmt.Printf("%d writes committed under the lease\n", *writes)
}
