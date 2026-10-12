package tunnel

import (
	"context"
	"sync"
	"time"
)

const (
	synEntryTTL  = 30 * time.Second
	closingTTL   = 60 * time.Second
	evictionTick = 15 * time.Second
)

type portState struct {
	closingSince time.Time
}

func (s portState) closing() bool {
	return !s.closingSince.IsZero()
}

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
	t.activePorts.Store(localPort, portState{})
}

func (t *connTracker) markClosing(localPort uint16) {
	value, ok := t.activePorts.Load(localPort)
	if !ok {
		return
	}
	if state, ok := value.(portState); ok && state.closing() {
		return
	}
	t.activePorts.Store(localPort, portState{closingSince: t.now()})
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
	now := t.now()
	synCutoff := now.Add(-synEntryTTL)
	t.outgoingSYNs.Range(func(key, value any) bool {
		if ts, ok := value.(time.Time); ok && ts.Before(synCutoff) {
			t.outgoingSYNs.Delete(key)
		}
		return true
	})

	closingCutoff := now.Add(-closingTTL)
	t.activePorts.Range(func(key, value any) bool {
		if state, ok := value.(portState); ok && state.closing() && state.closingSince.Before(closingCutoff) {
			t.activePorts.Delete(key)
		}
		return true
	})
}

func (t *connTracker) evictLoop(ctx context.Context) {
	ticker := time.NewTicker(evictionTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.evictStale()
		}
	}
}
