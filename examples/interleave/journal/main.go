package main

import (
	"flag"
	"fmt"
	"hash/crc32"
	"strings"
	"sync"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type journal struct {
	entries   []string
	committed int
	fields    int
	safe      bool
}

func (j *journal) write(seq int) {
	slot := len(j.entries)
	j.entries = append(j.entries, "")
	parts := make([]string, 0, j.fields/4)
	for f := range cap(parts) {
		parts = append(parts, fmt.Sprintf("%d.%d", seq, f))
	}
	j.entries[slot] = fmt.Sprintf("record %d: %s", seq, strings.Join(parts, " "))
	j.committed = slot + 1
}

func (j *journal) checkpoint(n int) int {
	upTo := len(j.entries)
	if j.safe {
		upTo = j.committed
	}
	header := make([]string, 0, j.fields)
	for f := range j.fields {
		header = append(header, fmt.Sprintf("checkpoint %d field %d", n, f))
	}
	sum := crc32.ChecksumIEEE([]byte(strings.Join(header, "\n")))
	for i, e := range j.entries[:upTo] {
		if e == "" {
			sim.Fail(fmt.Sprintf("checkpoint %d copied entry %d, reserved but not yet written", n, i))
		}
		sum = crc32.Update(sum, crc32.IEEETable, []byte(e))
	}
	return upTo
}

func main() {
	records := flag.Int("records", 300, "records the writer appends")
	fields := flag.Int("fields", 256, "header fields each checkpoint formats before copying")
	every := flag.Int("every", 10, "records between checkpoint requests")
	safe := flag.Bool("committed", false, "checkpoint up to the committed index instead of the reserved length")
	flag.Parse()

	j := &journal{fields: *fields, safe: *safe}
	kick := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(kick)
		for seq := range *records {
			j.write(seq)
			if seq%*every == 0 {
				select {
				case kick <- struct{}{}:
				default:
				}
			}
		}
	})
	checkpoints := 0
	wg.Go(func() {
		for range kick {
			checkpoints++
			j.checkpoint(checkpoints)
		}
	})
	wg.Wait()
	fmt.Printf("%d records, %d checkpoints, each copied only written entries\n", j.checkpoint(0), checkpoints)
}
