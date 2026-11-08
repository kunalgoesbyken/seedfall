package main

import (
	"flag"
	"fmt"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type shop struct {
	mu    sync.Mutex
	stock int
	sold  []int
	safe  bool
}

func (s *shop) buy(buyer int) bool {
	if s.safe {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	if s.stock == 0 {
		return false
	}
	item := s.stock
	receipt := fmt.Sprintf("buyer %d takes item %d", buyer, item)
	if prev := s.sold[item]; prev != 0 {
		sim.Fail(fmt.Sprintf("%s, already sold to buyer %d", receipt, prev))
	}
	s.sold[item] = buyer
	s.stock = item - 1
	return true
}

func main() {
	buyers := flag.Int("buyers", 4, "concurrent buyers")
	items := flag.Int("items", 400, "items in stock")
	safe := flag.Bool("lock", false, "hold a mutex across the check and the update")
	flag.Parse()

	s := &shop{stock: *items, sold: make([]int, *items+1), safe: *safe}
	var wg sync.WaitGroup
	for b := 1; b <= *buyers; b++ {
		wg.Go(func() {
			for s.buy(b) {
				time.Sleep(time.Millisecond)
			}
		})
	}
	wg.Wait()
	fmt.Printf("%d items sold once each\n", len(s.sold))
}
