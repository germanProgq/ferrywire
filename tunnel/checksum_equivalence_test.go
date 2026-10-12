package tunnel

import (
	"math/rand"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

func legacyTCPChecksum(tcpData []byte, srcIP, dstIP [4]byte) uint16 {
	pseudoHeader := []byte{
		srcIP[0], srcIP[1], srcIP[2], srcIP[3],
		dstIP[0], dstIP[1], dstIP[2], dstIP[3],
		0, 6,
		0, 0,
	}
	tcpLen := len(tcpData)
	pseudoHeader[10] = byte(tcpLen >> 8)
	pseudoHeader[11] = byte(tcpLen & 0xff)

	all := make([]byte, 0, len(pseudoHeader)+tcpLen)
	all = append(all, pseudoHeader...)
	all = append(all, tcpData...)

	sum := uint32(0)
	for i := 0; i < len(all)-1; i += 2 {
		sum += uint32(all[i])<<8 | uint32(all[i+1])
	}
	if len(all)%2 == 1 {
		sum += uint32(all[len(all)-1]) << 8
	}
	for sum>>16 > 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return uint16(^sum)
}

func legacyIPChecksum(b []byte) uint16 {
	sum := uint32(0)
	for i := 0; i < len(b)-1; i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	sum = (sum >> 16) + (sum & 0xFFFF)
	sum += sum >> 16
	return uint16(^sum)
}

func gvisorIPChecksum(b []byte) uint16 {
	return ^checksum.Checksum(b, 0)
}

func gvisorTCPChecksum(tcpData []byte, srcIP, dstIP [4]byte) uint16 {
	pseudo := header.PseudoHeaderChecksum(tcpProtocol, tcpip.AddrFrom4(srcIP), tcpip.AddrFrom4(dstIP), uint16(len(tcpData)))
	return ^checksum.Checksum(tcpData, pseudo)
}

func checksumInputs(rng *rand.Rand) [][]byte {
	inputs := [][]byte{
		make([]byte, 20),
		make([]byte, 21),
		make([]byte, 1),
		{0xFF},
		{0xFF, 0xFF, 0xFF},
		make([]byte, 1501),
	}
	for i := 0; i < 5000; i++ {
		buf := make([]byte, 1+rng.Intn(1600))
		rng.Read(buf)
		inputs = append(inputs, buf)
	}
	return inputs
}

func TestIPChecksumMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i, buf := range checksumInputs(rng) {
		if want, got := legacyIPChecksum(buf), gvisorIPChecksum(buf); want != got {
			t.Fatalf("input %d len=%d: legacy=%04x gvisor=%04x", i, len(buf), want, got)
		}
	}
}

func TestTCPChecksumMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i, buf := range checksumInputs(rng) {
		var src, dst [4]byte
		if i%3 != 0 {
			rng.Read(src[:])
			rng.Read(dst[:])
		}
		if want, got := legacyTCPChecksum(buf, src, dst), gvisorTCPChecksum(buf, src, dst); want != got {
			t.Fatalf("input %d len=%d: legacy=%04x gvisor=%04x", i, len(buf), want, got)
		}
	}
}

func TestRecomputeChecksumsMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 2000; i++ {
		pkt := make([]byte, 40+rng.Intn(1460))
		rng.Read(pkt)
		pkt[0] = 0x45

		recomputeChecksums(pkt)

		gotIP := uint16(pkt[10])<<8 | uint16(pkt[11])
		tcp := pkt[20:]
		gotTCP := uint16(tcp[16])<<8 | uint16(tcp[17])

		ipHeader := append([]byte{}, pkt[:20]...)
		ipHeader[10], ipHeader[11] = 0, 0
		tcpCopy := append([]byte{}, tcp...)
		tcpCopy[16], tcpCopy[17] = 0, 0
		var src, dst [4]byte
		copy(src[:], pkt[12:16])
		copy(dst[:], pkt[16:20])

		if want := legacyIPChecksum(ipHeader); want != gotIP {
			t.Fatalf("packet %d: IP checksum legacy=%04x got=%04x", i, want, gotIP)
		}
		if want := legacyTCPChecksum(tcpCopy, src, dst); want != gotTCP {
			t.Fatalf("packet %d: TCP checksum legacy=%04x got=%04x", i, want, gotTCP)
		}
	}
}
