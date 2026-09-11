package yandex

import (
	"bytes"
	"testing"
)

func TestBatchRoundTrip(t *testing.T) {
	in := [][]byte{
		{0x00, 0x45, 0x00, 0x28},
		{0x1F, 0x01, 0x02, 0x03, 0x04, 0x05},
		bytes.Repeat([]byte{0xAB}, 1500),
		{0x00},
	}

	framed := encodeBatch(in)
	if framed[0] != batchMarker {
		t.Fatalf("frame must start with batchMarker 0x%02x, got 0x%02x", batchMarker, framed[0])
	}

	out := decodeBatch(framed)
	if len(out) != len(in) {
		t.Fatalf("packet count: got %d, want %d", len(out), len(in))
	}
	for i := range in {
		if !bytes.Equal(out[i], in[i]) {
			t.Errorf("packet %d mismatch:\n got %v\nwant %v", i, out[i], in[i])
		}
	}
}

func TestBatchMarkerDistinctFromCompressor(t *testing.T) {
	if batchMarker == 0x00 || batchMarker == 0x1F {
		t.Fatalf("batchMarker 0x%02x collides with a compressor marker", batchMarker)
	}
}

func TestDecodeBatchRejectsTruncated(t *testing.T) {
	buf := []byte{batchMarker, 0x00, 0x10, 0x00, 0xAA}
	out := decodeBatch(buf)
	if len(out) != 0 {
		t.Fatalf("truncated frame should yield 0 packets, got %d", len(out))
	}
}

func TestDrainBatchStopsWhenEmpty(t *testing.T) {
	q := make(chan []byte, 8)
	q <- []byte{2}
	q <- []byte{3}
	got := drainBatch(q, []byte{1})
	if len(got) != 3 {
		t.Fatalf("got %d packets, want 3", len(got))
	}
	if len(q) != 0 {
		t.Fatalf("queue should be drained, %d left", len(q))
	}
}

func TestDrainBatchRespectsByteCap(t *testing.T) {
	q := make(chan []byte, 64)
	big := bytes.Repeat([]byte{0xEE}, maxBatchBytes/2)
	for i := 0; i < 10; i++ {
		q <- big
	}
	got := drainBatch(q, big)
	if len(got) < 1 {
		t.Fatal("must always include the first packet")
	}
	total := 0
	for _, p := range got {
		total += lenPrefixSize + len(p)
	}
	if total > maxBatchBytes+lenPrefixSize+len(big) {
		t.Fatalf("batch too large: %d bytes", total)
	}
}
