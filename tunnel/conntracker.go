package tunnel

import (
	"sync"
	"time"
)

const synEntryTTL = 30 * time.Second

type connTracker struct {
	activePorts  sync.Map
	outgoingSYNs sync.Map
	now          func() time.Time
}

func newConnTracker() *connTracker {
	return &connTracker{now: time.Now}
}

func (t *connTracker) recordSYN(seq uint32, localPort uint16) {
	t.outgoingSYNs.Store(seq, t.now())
	t.activePorts.Store(localPort, struct{}{})
}

func (t *connTracker) closePort(localPort uint16) {
	t.activePorts.Delete(localPort)
}

func (t *connTracker) isActive(localPort uint16) bool {
	_, ok := t.activePorts.Load(localPort)
	return ok
}

func (t *connTracker) consumeSYNACK(synSeq uint32) bool {
	if _, ok := t.outgoingSYNs.Load(synSeq); !ok {
		return false
	}
	t.outgoingSYNs.Delete(synSeq)
	return true
}

func (t *connTracker) evictStale() {
	cutoff := t.now().Add(-synEntryTTL)
	t.outgoingSYNs.Range(func(key, value any) bool {
		if ts, ok := value.(time.Time); ok && ts.Before(cutoff) {
			t.outgoingSYNs.Delete(key)
		}
		return true
	})
}

func (t *connTracker) evictLoop() {
	ticker := time.NewTicker(synEntryTTL)
	defer ticker.Stop()
	for range ticker.C {
		t.evictStale()
	}
}
