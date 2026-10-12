package tunnel

import (
	"testing"
	"time"
)

func TestConnTrackerClosesLocalPort(t *testing.T) {
	tr := newConnTracker()
	tr.recordSYN(1000, 51000)

	if !tr.isActive(51000) {
		t.Fatal("local port should be active after SYN")
	}

	tr.closePort(51000)
	if tr.isActive(51000) {
		t.Fatal("local port should be inactive after close")
	}
}

func TestConnTrackerConsumeSYNACK(t *testing.T) {
	tr := newConnTracker()
	tr.recordSYN(42, 40000)

	if !tr.consumeSYNACK(42) {
		t.Fatal("first matching SYN-ACK should be accepted")
	}
	if tr.consumeSYNACK(42) {
		t.Fatal("SYN-ACK must be single-use")
	}
	if tr.consumeSYNACK(99) {
		t.Fatal("unknown SYN-ACK must be rejected")
	}
}

func TestConnTrackerEvictsStaleSYNs(t *testing.T) {
	now := time.Unix(0, 0)
	tr := newConnTracker()
	tr.now = func() time.Time { return now }

	tr.recordSYN(7, 30000)

	now = now.Add(synEntryTTL / 2)
	tr.evictStale()
	if !tr.consumeSYNACK(7) {
		t.Fatal("SYN within TTL must survive eviction")
	}

	tr.recordSYN(8, 30001)
	now = now.Add(synEntryTTL * 2)
	tr.evictStale()
	if tr.consumeSYNACK(8) {
		t.Fatal("stale SYN must be evicted")
	}
}
