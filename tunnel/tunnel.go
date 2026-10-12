package tunnel

import (
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"

	"github.com/germanProgq/ferrywire/transport"
)

const (
	tunnelNICID   = tcpip.NICID(1)
	internetNICID = tcpip.NICID(2)
)

type TCPTunnel struct {
	gvisorStack *stack.Stack
	tunnelEP    *TunnelLinkEndpoint
	transport   transport.Transport
	isExitNode  bool
	rawEP       *RawSocketEndpoint
	ownsRSTRule bool
	startTime   time.Time
	packetCount atomic.Uint64
}

func NewTCPTunnel(trans transport.Transport, isExitNode bool) *TCPTunnel {
	t := &TCPTunnel{
		transport:  trans,
		isExitNode: isExitNode,
		startTime:  time.Now(),
	}

	slog.Debug("[TUNNEL] Net stack init...")
	t.gvisorStack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	if err := t.gvisorStack.SetTransportProtocolOption(tcp.ProtocolNumber,
		&tcpip.TCPReceiveBufferSizeRangeOption{Min: 65536, Default: 262144, Max: 1048576}); err != nil {
		slog.Debug(fmt.Sprintf("[TUNNEL] Failed to set recv buffer: %v", err))
	}
	if err := t.gvisorStack.SetTransportProtocolOption(tcp.ProtocolNumber,
		&tcpip.TCPSendBufferSizeRangeOption{Min: 65536, Default: 262144, Max: 1048576}); err != nil {
		slog.Debug(fmt.Sprintf("[TUNNEL] Failed to set send buffer: %v", err))
	}

	tunnelEP := NewTunnelLinkEndpoint()
	tunnelEP.onOutgoingPacket = func(data []byte) {
		if err := trans.Send(data); err != nil {
			slog.Debug(fmt.Sprintf("[TUNNEL] transport Send dropped %d bytes: %v", len(data), err))
		}
	}
	t.tunnelEP = tunnelEP

	tunnelNIC := tunnelNICID
	if err := t.gvisorStack.CreateNIC(tunnelNIC, tunnelEP); err != nil {
		slog.Debug(fmt.Sprintf("[TUNNEL] CreateNIC tunnel error: %v", err))
	}

	if isExitNode {
		t.setupExitNode(tunnelNIC)
	} else {
		t.setupClient(tunnelNIC)
	}

	trans.Receive(func(data []byte) {
		tunnelEP.InjectInbound(data)
	})

	go t.printStats()
	return t
}

func (t *TCPTunnel) setupExitNode(tunnelNIC tcpip.NICID) {
	localIP := getLocalIP()
	slog.Debug(fmt.Sprintf("[TUNNEL] EXIT NODE - Local IP: %s", localIP))

	t.ownsRSTRule = ensureRSTDropRule()

	rawEP, err := NewRawSocketEndpoint(internetNICID)
	if err != nil {
		slog.Debug(fmt.Sprintf("[TUNNEL] Raw socket error: %v", err))
		return
	}

	t.rawEP = rawEP
	rawEP.SetTransportSender(func(data []byte) {
		t.transport.Send(data)
	})

	internetNIC := internetNICID
	if err := t.gvisorStack.CreateNIC(internetNIC, rawEP); err != nil {
		slog.Debug(fmt.Sprintf("[TUNNEL] CreateNIC internet error: %v", err))
		return
	}

	var ipBytes [4]byte
	fmt.Sscanf(localIP, "%d.%d.%d.%d", &ipBytes[0], &ipBytes[1], &ipBytes[2], &ipBytes[3])
	internetAddr := tcpip.AddrFrom4(ipBytes)
	t.gvisorStack.AddProtocolAddress(internetNIC, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   internetAddr,
			PrefixLen: 24,
		},
	}, stack.AddressProperties{})

	t.gvisorStack.SetForwardingDefaultAndAllNICs(ipv4.ProtocolNumber, true)
	t.gvisorStack.AddRoute(tcpip.Route{
		Destination: header.IPv4EmptySubnet,
		NIC:         internetNIC,
	})

	tunnelSubnet := tcpip.AddressWithPrefix{
		Address:   tcpip.AddrFrom4([4]byte{10, 10, 10, 0}),
		PrefixLen: 24,
	}.Subnet()
	t.gvisorStack.AddRoute(tcpip.Route{
		Destination: tunnelSubnet,
		NIC:         tunnelNIC,
	})
}

func (t *TCPTunnel) setupClient(tunnelNIC tcpip.NICID) {
	clientAddr := tcpip.AddrFrom4([4]byte{10, 10, 10, 2})
	t.gvisorStack.AddProtocolAddress(tunnelNIC, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   clientAddr,
			PrefixLen: 24,
		},
	}, stack.AddressProperties{})

	t.gvisorStack.AddRoute(tcpip.Route{
		Destination: header.IPv4EmptySubnet,
		NIC:         tunnelNIC,
	})
}

func (t *TCPTunnel) Close() {
	if t.rawEP != nil {
		t.rawEP.Close()
	}
	if t.ownsRSTRule {
		removeRSTDropRule()
		t.ownsRSTRule = false
	}
}

func (t *TCPTunnel) DialTCP(address string) (net.Conn, error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}

	ip := tcpAddr.IP.To4()
	if ip == nil {
		return nil, fmt.Errorf("IPv6 not supported")
	}

	nic := tunnelNICID
	if t.isExitNode {
		nic = internetNICID
	}

	conn, err := gonet.DialTCP(t.gvisorStack, tcpip.FullAddress{
		NIC:  nic,
		Addr: tcpip.AddrFrom4([4]byte{ip[0], ip[1], ip[2], ip[3]}),
		Port: uint16(tcpAddr.Port),
	}, ipv4.ProtocolNumber)

	return conn, err
}

func (t *TCPTunnel) printStats() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := t.gvisorStack.Stats()
		slog.Debug(fmt.Sprintf("[STATS] uptime=%v packets=%d connected=%d established=%d retrans=%d",
			time.Since(t.startTime).Round(time.Second),
			t.packetCount.Load(),
			stats.TCP.CurrentConnected.Value(),
			stats.TCP.CurrentEstablished.Value(),
			stats.TCP.Retransmits.Value(),
		))
	}
}

var (
	cachedLocalIP     string
	cachedLocalIPOnce sync.Once
)

func getLocalIP() string {
	cachedLocalIPOnce.Do(func() {
		cachedLocalIP = lookupLocalIP()
	})
	return cachedLocalIP
}

func lookupLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "192.168.1.100"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
