package tunnel

import (
	"fmt"
	"net"
	"strings"
)

func parsePacketInfo(data []byte) string {
	if len(data) < 20 {
		return fmt.Sprintf("short packet (%d bytes)", len(data))
	}
	srcIP := net.IP(data[12:16])
	dstIP := net.IP(data[16:20])
	protocol := data[9]
	ttl := data[8]
	totalLen := uint16(data[2])<<8 | uint16(data[3])

	if protocol == tcpProtocol && len(data) >= minIPv4TCPHeader {
		srcPort := uint16(data[20])<<8 | uint16(data[21])
		dstPort := uint16(data[22])<<8 | uint16(data[23])
		flags := data[33]
		seq := uint32(data[24])<<24 | uint32(data[25])<<16 | uint32(data[26])<<8 | uint32(data[27])
		ack := uint32(data[28])<<24 | uint32(data[29])<<16 | uint32(data[30])<<8 | uint32(data[31])
		window := uint16(data[34])<<8 | uint16(data[35])

		return fmt.Sprintf("TCP %s:%d -> %s:%d [%s] seq=%d ack=%d win=%d len=%d ttl=%d",
			srcIP, srcPort, dstIP, dstPort, tcpFlagString(flags), seq, ack, window, totalLen, ttl)
	}
	return fmt.Sprintf("IP proto=%d %s -> %s len=%d ttl=%d", protocol, srcIP, dstIP, totalLen, ttl)
}

func tcpFlagString(flags byte) string {
	var names []string
	if flags&synFlag != 0 {
		names = append(names, "SYN")
	}
	if flags&0x10 != 0 {
		names = append(names, "ACK")
	}
	if flags&finFlag != 0 {
		names = append(names, "FIN")
	}
	if flags&rstFlag != 0 {
		names = append(names, "RST")
	}
	if flags&0x08 != 0 {
		names = append(names, "PSH")
	}
	return strings.Join(names, " ")
}
