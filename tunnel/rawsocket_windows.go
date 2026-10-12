package tunnel

import (
	"fmt"

	"golang.org/x/sys/windows"
)

const protoRaw = 255

type windowsRawSocket struct {
	sendFd windows.Handle
	recvFd windows.Handle
}

func openRawSocket() (rawSocket, error) {
	sendFd, err := windows.Socket(windows.AF_INET, windows.SOCK_RAW, protoRaw)
	if err != nil {
		return nil, fmt.Errorf("send socket failed: %v (need admin)", err)
	}

	if err := windows.SetsockoptInt(sendFd, windows.IPPROTO_IP, windows.IP_HDRINCL, 1); err != nil {
		windows.Close(sendFd)
		return nil, fmt.Errorf("IP_HDRINCL: %v", err)
	}

	recvFd, err := windows.Socket(windows.AF_INET, windows.SOCK_RAW, windows.IPPROTO_TCP)
	if err != nil {
		windows.Close(sendFd)
		return nil, fmt.Errorf("recv socket failed: %v (need admin)", err)
	}

	if err := windows.Bind(recvFd, &windows.SockaddrInet4{Addr: [4]byte{0, 0, 0, 0}, Port: 0}); err != nil {
		windows.Close(sendFd)
		windows.Close(recvFd)
		return nil, fmt.Errorf("bind failed: %v", err)
	}

	return &windowsRawSocket{sendFd: sendFd, recvFd: recvFd}, nil
}

func (s *windowsRawSocket) recv(buf []byte) (int, error) {
	n, _, err := windows.Recvfrom(s.recvFd, buf, 0)
	if err != nil {
		if err == windows.WSAEWOULDBLOCK {
			return 0, errWouldBlock
		}
		return 0, err
	}
	return n, nil
}

func (s *windowsRawSocket) send(packet []byte, dst [4]byte) error {
	return windows.Sendto(s.sendFd, packet, 0, &windows.SockaddrInet4{Addr: dst, Port: 0})
}

func (s *windowsRawSocket) close() error {
	windows.Close(s.sendFd)
	return windows.Close(s.recvFd)
}
