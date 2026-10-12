package transport

import (
	"bytes"
	"testing"
)

func TestCompressRoundTrip(t *testing.T) {
	cases := map[string][]byte{
		"empty":             {},
		"tiny":              {0x45, 0x00, 0x28},
		"below threshold":   bytes.Repeat([]byte{0xAB}, MinCompressSize),
		"compressible":      bytes.Repeat([]byte{0x42}, 4096),
		"incompressible":    randomBytes(4096),
		"exact marker byte": {CompressionMarker},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := decompress(compress(in))
			if err != nil {
				t.Fatalf("decompress: %v", err)
			}
			if !bytes.Equal(out, in) {
				t.Fatalf("round trip mismatch: got %v want %v", out, in)
			}
		})
	}
}

func TestCompressPicksSmallerEncoding(t *testing.T) {
	compressible := bytes.Repeat([]byte{0x00}, 4096)
	if got := compress(compressible); got[0] != CompressionMarker {
		t.Fatalf("highly compressible payload should use the lz4 marker, got 0x%02x", got[0])
	}

	incompressible := randomBytes(4096)
	if got := compress(incompressible); got[0] != 0x00 {
		t.Fatalf("incompressible payload should fall back to the raw marker, got 0x%02x", got[0])
	}
}

func TestCompressedLoopback(t *testing.T) {
	a := &loopback{}
	b := &loopback{}
	a.peer = b
	b.peer = a

	client := NewCompressed(a)
	exit := NewCompressed(b)

	var got [][]byte
	exit.Receive(func(data []byte) {
		got = append(got, append([]byte{}, data...))
	})

	payloads := [][]byte{
		{0x45, 0x00, 0x28},
		bytes.Repeat([]byte{0x7E}, 8192),
		randomBytes(2048),
	}
	for _, p := range payloads {
		if err := client.Send(p); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	if len(got) != len(payloads) {
		t.Fatalf("received %d payloads, want %d", len(got), len(payloads))
	}
	for i := range payloads {
		if !bytes.Equal(got[i], payloads[i]) {
			t.Fatalf("payload %d corrupted over loopback", i)
		}
	}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	seed := uint32(2463534242)
	for i := range b {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		b[i] = byte(seed)
	}
	return b
}

type loopback struct {
	cb   func([]byte)
	peer *loopback
}

func (l *loopback) Start() error            { return nil }
func (l *loopback) Receive(cb func([]byte)) { l.cb = cb }
func (l *loopback) Send(data []byte) error {
	if l.peer.cb != nil {
		l.peer.cb(append([]byte{}, data...))
	}
	return nil
}
