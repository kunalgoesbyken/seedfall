package engine

import (
	"testing"

	"github.com/tetratelabs/wazero/api"
)

type memoryModule struct {
	api.Module
	mem *fakeMemory
}

func (m memoryModule) Memory() api.Memory { return m.mem }

func TestReadSubscriptionsRejectsACountThatOverflowsTheByteSize(t *testing.T) {
	w := &world{}
	m := memoryModule{mem: &fakeMemory{b: make([]byte, 4*subscriptionSize)}}
	subs, ok := w.readSubscriptions(m, 0, 4, nil)
	if !ok || len(subs) != 4 {
		t.Fatalf("four subscriptions: %d, %v", len(subs), ok)
	}
	wraps := uint32(1<<32/subscriptionSize + 1)
	if wraps*subscriptionSize > uint32(len(m.mem.b)) {
		t.Fatalf("count %d does not wrap to a readable size", wraps)
	}
	if subs, ok := w.readSubscriptions(m, 0, wraps, nil); ok || subs != nil {
		t.Fatalf("a count of %d read %d subscriptions, want errnoFault", wraps, len(subs))
	}
}
