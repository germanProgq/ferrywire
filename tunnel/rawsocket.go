package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	tcpProtocol      = 6
	synAckFlags      = 0x12
	synFlag          = 0x02
	finFlag          = 0x01
	rstFlag          = 0x04
	minIPv4TCPHeader = 40
	readTimeout      = 10 * time.Millisecond
)

var (
	errWouldBlock   = errors.New("raw socket read would block")
	clientVirtualIP = [4]byte{10, 10, 10, 2}
)

type rawSocket interface {
	send(packet []byte, dst [4]byte) error
	recv(buf []byte) (int, error)
	close() error
}

type RawSocketEndpoint struct {
	dispatcher      stack.NetworkDispatcher
	sock            rawSocket
	nicID           tcpip.NICID
	packetOut       atomic.Uint64
	tracker         *connTracker
	sendToTransport func([]byte)
	stopEviction    context.CancelFunc
	closeOnce       sync.Once
}

func NewRawSocketEndpoint(nicID tcpip.NICID) (*RawSocketEndpoint, error) {
	sock, err := openRawSocket()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	ep := &RawSocketEndpoint{
		sock:         sock,
		nicID:        nicID,
		tracker:      newConnTracker(),
		stopEviction: cancel,
	}
	go ep.readLoop()
	go ep.tracker.evictLoop(ctx)
	return ep, nil
}

func (e *RawSocketEndpoint) SetTransportSender(sendFunc func([]byte)) {
	e.sendToTransport = sendFunc
}

func (e *RawSocketEndpoint) readLoop() {
	buf := make([]byte, 65535)
	for {
		n, err := e.sock.recv(buf)
		if err != nil {
			if errors.Is(err, errWouldBlock) {
				time.Sleep(readTimeout)
				continue
			}
			slog.Debug(fmt.Sprintf("[RAW-NIC%d] Read error: %v", e.nicID, err))
			return
		}
		if n < minIPv4TCPHeader {
			continue
		}
		if pkt := e.rewriteInbound(buf[:n]); pkt != nil && e.sendToTransport != nil {
			e.sendToTransport(pkt)
		}
	}
}

func (e *RawSocketEndpoint) rewriteInbound(raw []byte) []byte {
	if raw[9] != tcpProtocol {
		return nil
	}
	if net.IP(raw[16:20]).String() != getLocalIP() {
		return nil
	}

	dstPort := uint16(raw[22])<<8 | uint16(raw[23])
	if !e.tracker.isActive(dstPort) {
		return nil
	}

	if raw[33] == synAckFlags {
		ackNum := uint32(raw[28])<<24 | uint32(raw[29])<<16 | uint32(raw[30])<<8 | uint32(raw[31])
		if !e.tracker.consumeSYNACK(ackNum - 1) {
			return nil
		}
	}

	pkt := make([]byte, len(raw))
	copy(pkt, raw)
	setDestIP(pkt, clientVirtualIP)
	return pkt
}

func (e *RawSocketEndpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	written := 0
	for _, pb := range pkts.AsSlice() {
		ipPacket := pb.ToView().ToSlice()
		if len(ipPacket) < minIPv4TCPHeader {
			continue
		}

		pkt := make([]byte, len(ipPacket))
		copy(pkt, ipPacket)
		setSourceIP(pkt, localIPBytes())

		e.trackOutbound(pkt[ipHeaderLen(pkt):])

		var dst [4]byte
		copy(dst[:], pkt[16:20])
		if err := e.sock.send(pkt, dst); err != nil {
			slog.Debug(fmt.Sprintf("[RAW-NIC%d] Sendto failed: %v", e.nicID, err))
			continue
		}
		e.packetOut.Add(1)
		written++
	}
	return written, nil
}

func (e *RawSocketEndpoint) trackOutbound(tcpHeader []byte) {
	srcPort := uint16(tcpHeader[0])<<8 | uint16(tcpHeader[1])
	flags := tcpHeader[13]
	switch {
	case flags&rstFlag != 0:
		e.tracker.closePort(srcPort)
	case flags&synFlag != 0:
		seq := uint32(tcpHeader[4])<<24 | uint32(tcpHeader[5])<<16 | uint32(tcpHeader[6])<<8 | uint32(tcpHeader[7])
		e.tracker.recordSYN(seq, srcPort)
	case flags&finFlag != 0:
		e.tracker.markClosing(srcPort)
	}
}

func ipHeaderLen(pkt []byte) int {
	return int(pkt[0]&0x0F) * 4
}

func setSourceIP(pkt []byte, ip [4]byte) {
	copy(pkt[12:16], ip[:])
	recomputeChecksums(pkt)
}

func setDestIP(pkt []byte, ip [4]byte) {
	copy(pkt[16:20], ip[:])
	recomputeChecksums(pkt)
}

func recomputeChecksums(pkt []byte) {
	pkt[10], pkt[11] = 0, 0
	ipSum := ^checksum.Checksum(pkt[:20], 0)
	pkt[10] = byte(ipSum >> 8)
	pkt[11] = byte(ipSum & 0xFF)

	tcpHeader := pkt[ipHeaderLen(pkt):]
	var src, dst [4]byte
	copy(src[:], pkt[12:16])
	copy(dst[:], pkt[16:20])
	tcpHeader[16], tcpHeader[17] = 0, 0
	pseudo := header.PseudoHeaderChecksum(tcpProtocol, tcpip.AddrFrom4(src), tcpip.AddrFrom4(dst), uint16(len(tcpHeader)))
	tcpSum := ^checksum.Checksum(tcpHeader, pseudo)
	tcpHeader[16] = byte(tcpSum >> 8)
	tcpHeader[17] = byte(tcpSum & 0xFF)
}

func localIPBytes() [4]byte {
	var b [4]byte
	fmt.Sscanf(getLocalIP(), "%d.%d.%d.%d", &b[0], &b[1], &b[2], &b[3])
	return b
}

func (e *RawSocketEndpoint) MTU() uint32                    { return 1500 }
func (e *RawSocketEndpoint) MaxHeaderLength() uint16        { return 0 }
func (e *RawSocketEndpoint) LinkAddress() tcpip.LinkAddress { return "" }
func (e *RawSocketEndpoint) Capabilities() stack.LinkEndpointCapabilities {
	return stack.CapabilityNone
}
func (e *RawSocketEndpoint) Attach(dispatcher stack.NetworkDispatcher) { e.dispatcher = dispatcher }
func (e *RawSocketEndpoint) IsAttached() bool                          { return e.dispatcher != nil }
func (e *RawSocketEndpoint) Wait()                                     {}
func (e *RawSocketEndpoint) ARPHardwareType() header.ARPHardwareType   { return header.ARPHardwareNone }
func (e *RawSocketEndpoint) AddHeader(*stack.PacketBuffer)             {}
func (e *RawSocketEndpoint) Close() {
	e.closeOnce.Do(func() {
		e.stopEviction()
		e.sock.close()
	})
}
func (e *RawSocketEndpoint) SetMTU(uint32)                        {}
func (e *RawSocketEndpoint) SetLinkAddress(tcpip.LinkAddress)     {}
func (e *RawSocketEndpoint) ParseHeader(*stack.PacketBuffer) bool { return true }
func (e *RawSocketEndpoint) SetOnCloseAction(func())              {}
