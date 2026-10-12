package tunnel

import (
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

func sampleTCPPacket() []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	pkt[2], pkt[3] = 0x00, 0x28
	pkt[8] = 64
	pkt[9] = tcpProtocol
	copy(pkt[12:16], []byte{192, 168, 1, 50})
	copy(pkt[16:20], []byte{93, 184, 216, 34})

	tcp := pkt[20:]
	tcp[0], tcp[1] = 0xC0, 0x00
	tcp[2], tcp[3] = 0x01, 0xBB
	tcp[12] = 0x50
	tcp[13] = synFlag
	tcp[14], tcp[15] = 0xFF, 0xFF
	return pkt
}

func ipChecksumValid(pkt []byte) bool {
	return checksum.Checksum(pkt[:20], 0) == 0xFFFF
}

func tcpChecksumValid(pkt []byte) bool {
	tcp := pkt[ipHeaderLen(pkt):]
	var src, dst [4]byte
	copy(src[:], pkt[12:16])
	copy(dst[:], pkt[16:20])
	pseudo := header.PseudoHeaderChecksum(tcpProtocol, tcpip.AddrFrom4(src), tcpip.AddrFrom4(dst), uint16(len(tcp)))
	return checksum.Checksum(tcp, pseudo) == 0xFFFF
}

func TestSetDestIPRewritesAndChecksums(t *testing.T) {
	pkt := sampleTCPPacket()
	setDestIP(pkt, clientVirtualIP)

	for i, b := range clientVirtualIP {
		if pkt[16+i] != b {
			t.Fatalf("dst IP byte %d = %d, want %d", i, pkt[16+i], b)
		}
	}
	if !ipChecksumValid(pkt) {
		t.Fatal("IP checksum invalid after setDestIP")
	}
	if !tcpChecksumValid(pkt) {
		t.Fatal("TCP checksum invalid after setDestIP")
	}
}

func TestSetSourceIPRewritesAndChecksums(t *testing.T) {
	pkt := sampleTCPPacket()
	newIP := [4]byte{10, 0, 0, 7}
	setSourceIP(pkt, newIP)

	for i, b := range newIP {
		if pkt[12+i] != b {
			t.Fatalf("src IP byte %d = %d, want %d", i, pkt[12+i], b)
		}
	}
	if !ipChecksumValid(pkt) {
		t.Fatal("IP checksum invalid after setSourceIP")
	}
	if !tcpChecksumValid(pkt) {
		t.Fatal("TCP checksum invalid after setSourceIP")
	}
}

func TestRewriteInboundFiltersAndRewrites(t *testing.T) {
	e := &RawSocketEndpoint{tracker: newConnTracker()}

	pkt := sampleTCPPacket()
	pkt[9] = 17
	if e.rewriteInbound(pkt) != nil {
		t.Fatal("non-TCP packet must be dropped")
	}

	pkt = sampleTCPPacket()
	if e.rewriteInbound(pkt) != nil {
		t.Fatal("packet for a non-local destination must be dropped")
	}
}

func TestParsePacketInfoFlags(t *testing.T) {
	pkt := sampleTCPPacket()
	pkt[33] = synFlag | 0x10
	info := parsePacketInfo(pkt)
	if info == "" {
		t.Fatal("expected non-empty packet info")
	}
}

func outboundSegment(localPort uint16, flags byte) []byte {
	pkt := sampleTCPPacket()
	tcp := pkt[20:]
	tcp[0], tcp[1] = byte(localPort>>8), byte(localPort)
	tcp[13] = flags
	return tcp
}

func inboundSegment(localPort uint16, flags byte) []byte {
	pkt := sampleTCPPacket()
	local := localIPBytes()
	copy(pkt[12:16], []byte{93, 184, 216, 34})
	copy(pkt[16:20], local[:])
	tcp := pkt[20:]
	tcp[0], tcp[1] = 0x01, 0xBB
	tcp[2], tcp[3] = byte(localPort>>8), byte(localPort)
	tcp[13] = flags
	return pkt
}

func TestInboundAcceptedAfterOutboundFIN(t *testing.T) {
	e := &RawSocketEndpoint{tracker: newConnTracker()}
	const port = 50123

	e.trackOutbound(outboundSegment(port, synFlag))
	e.trackOutbound(outboundSegment(port, finFlag|0x10))

	if e.rewriteInbound(inboundSegment(port, 0x10)) == nil {
		t.Fatal("ACK of our FIN must still be forwarded")
	}
	if e.rewriteInbound(inboundSegment(port, finFlag|0x10)) == nil {
		t.Fatal("remote FIN after our FIN must still be forwarded")
	}
}

func TestInboundDroppedAfterOutboundRST(t *testing.T) {
	e := &RawSocketEndpoint{tracker: newConnTracker()}
	const port = 50124

	e.trackOutbound(outboundSegment(port, synFlag))
	e.trackOutbound(outboundSegment(port, rstFlag))

	if e.rewriteInbound(inboundSegment(port, 0x10)) != nil {
		t.Fatal("packets after our RST must be dropped")
	}
}
