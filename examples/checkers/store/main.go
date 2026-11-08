package main

import (
	"flag"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
)

type store struct {
	mu    sync.Mutex
	cell  sync.Mutex
	racy  bool
	delay time.Duration
	data  map[string][]string
}

func (s *store) atomically(keys []string, fn func(view map[string][]string)) {
	if !s.racy {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	view := map[string][]string{}
	s.cell.Lock()
	for _, k := range keys {
		view[k] = slices.Clone(s.data[k])
	}
	s.cell.Unlock()
	s.pause()
	fn(view)
	s.cell.Lock()
	for _, k := range keys {
		s.data[k] = view[k]
	}
	s.cell.Unlock()
}

func (s *store) pause() { time.Sleep(time.Duration(sim.Random() % uint64(2*s.delay+1))) }

func (s *store) at(key string, fn func(l []string) []string) {
	s.cell.Lock()
	defer s.cell.Unlock()
	s.data[key] = fn(slices.Clone(s.data[key]))
}

func (s *store) one(key string, fn func(l []string) []string) {
	s.atomically([]string{key}, func(view map[string][]string) { view[key] = fn(view[key]) })
}

type workload struct {
	op    func(s *store, client, i int)
	drain func(s *store, client, ops int)
}

var workloads = map[string]workload{
	"queue": {queue, func(s *store, client, ops int) {
		for range ops {
			dequeue(s, client)
		}
	}},
	"cas":     {cas, nil},
	"counter": {counter, func(s *store, client, _ int) { counter(s, client, readEvery-1) }},
	"set":     {set, func(s *store, client, _ int) { set(s, client, readEvery-1) }},
	"append":  {appendLog, func(s *store, client, _ int) { appendLog(s, client, readEvery-1) }},
	"txn": {txn, func(s *store, client, _ int) {
		mops := []sim.Mop{sim.Read("x"), sim.Read("y"), sim.Read("z")}
		op := sim.Txn(client, mops...)
		s.atomically([]string{"x", "y", "z"}, func(view map[string][]string) {
			for i := range mops {
				mops[i].Value = slices.Clone(view[mops[i].Key])
			}
		})
		op.Ok(mops)
	}},
}

const readEvery = 3

func queue(s *store, client, i int) {
	if (client+i)%2 == 0 {
		v := fmt.Sprintf("%d-%d", client, i)
		op := sim.Invoke(client, "enqueue", "q", v)
		s.one("q", func(l []string) []string { return append(l, v) })
		op.Ok(nil)
		return
	}
	dequeue(s, client)
}

func dequeue(s *store, client int) {
	op := sim.Invoke(client, "dequeue", "q", nil)
	got := ""
	s.one("q", func(l []string) []string {
		if len(l) == 0 {
			return l
		}
		got = l[0]
		return l[1:]
	})
	op.Ok(got)
}

func cas(s *store, client, _ int) {
	me := strconv.Itoa(client)
	if swap(s, client, "", me) {
		time.Sleep(s.delay)
		swap(s, client, me, "")
	}
}

func swap(s *store, client int, old, next string) bool {
	op := sim.Invoke(client, "cas", "lock", []string{old, next})
	swapped := false
	s.one("lock", func(l []string) []string {
		if slices.Equal(l, single(old)) {
			swapped = true
			return single(next)
		}
		return l
	})
	op.Ok(swapped)
	return swapped
}

func counter(s *store, client, i int) {
	if i%readEvery == readEvery-1 {
		op := sim.Invoke(client, "read", "n", nil)
		var n int
		s.one("n", func(l []string) []string { n = total(l); return l })
		op.Ok(n)
		return
	}
	op := sim.Invoke(client, "add", "n", client)
	s.one("n", func(l []string) []string { return []string{strconv.Itoa(total(l) + client)} })
	op.Ok(nil)
}

func set(s *store, client, i int) {
	if i%readEvery == readEvery-1 {
		op := sim.Invoke(client, "read", "s", nil)
		var got []string
		s.one("s", func(l []string) []string { got = slices.Clone(l); return l })
		op.Ok(got)
		return
	}
	v := fmt.Sprintf("%d-%d", client, i)
	op := sim.Invoke(client, "add", "s", v)
	s.one("s", func(l []string) []string {
		if slices.Contains(l, v) {
			return l
		}
		return append(l, v)
	})
	op.Ok(nil)
}

func appendLog(s *store, client, i int) {
	if i%readEvery == readEvery-1 {
		op := sim.Invoke(client, "read", "log", nil)
		var got []string
		s.one("log", func(l []string) []string { got = slices.Clone(l); return l })
		op.Ok(got)
		return
	}
	v := fmt.Sprintf("%d-%d", client, i)
	op := sim.Invoke(client, "append", "log", v)
	s.one("log", func(l []string) []string { return append(l, v) })
	op.Ok(nil)
}

func txn(s *store, client, i int) {
	keys := []string{"x", "y", "z"}
	readKey, writeKey := keys[(client+i)%len(keys)], keys[(client+2*i+1)%len(keys)]
	v := fmt.Sprintf("%d-%d", client, i)
	mops := []sim.Mop{sim.Read(readKey), sim.Append(writeKey, v), sim.Read(writeKey)}
	op := sim.Txn(client, mops...)
	read := func(l []string) []string { mops[0].Value = slices.Clone(l); return l }
	write := func(l []string) []string {
		l = append(l, v)
		mops[2].Value = slices.Clone(l)
		return l
	}
	if s.racy {
		s.at(readKey, read)
		s.pause()
		s.at(writeKey, write)
	} else {
		s.atomically(slices.Compact([]string{readKey, writeKey}), func(view map[string][]string) {
			view[readKey] = read(view[readKey])
			view[writeKey] = write(view[writeKey])
		})
	}
	op.Ok(mops)
}

func single(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

func total(l []string) int {
	if len(l) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(l[0])
	return n
}

func main() {
	model := flag.String("model", "queue", "queue, cas, counter, set, append or txn")
	racy := flag.Bool("racy", false, "read, wait for storage, then write, without holding the lock across the three; for txn, run each micro-op atomically but not the transaction")
	clients := flag.Int("clients", 4, "concurrent clients")
	ops := flag.Int("ops", 30, "operations per client")
	delay := flag.Duration("delay", time.Millisecond, "mean simulated storage round trip inside every operation, drawn from the seed")
	flag.Parse()
	w, ok := workloads[*model]
	if !ok {
		sim.Fail("unknown model " + *model)
	}
	s := &store{racy: *racy, delay: *delay, data: map[string][]string{}}
	var wg sync.WaitGroup
	for c := 1; c <= *clients; c++ {
		wg.Go(func() {
			for i := range *ops {
				w.op(s, c, i)
			}
		})
	}
	wg.Wait()
	if w.drain != nil {
		w.drain(s, 0, *clients**ops)
	}
}
