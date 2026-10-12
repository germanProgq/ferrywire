//go:build linux || darwin

package tunnel

import (
	"fmt"
	"syscall"
)

const socketBufferSize = 4 * 1024 * 1024

type unixRawSocket struct {
	sendFd int
	recvFd int
}

func openRawSocket() (rawSocket, error) {
	sendFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_RAW)
	if err != nil {
		return nil, fmt.Errorf("send socket failed: %v (need root)", err)
	}

	if err := syscall.SetsockoptInt(sendFd, syscall.IPPROTO_IP, syscall.IP_HDRINCL, 1); err != nil {
		syscall.Close(sendFd)
		return nil, fmt.Errorf("IP_HDRINCL: %v", err)
	}

	recvFd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_TCP)
	if err != nil {
		syscall.Close(sendFd)
		return nil, fmt.Errorf("recv socket failed: %v (need root)", err)
	}

	syscall.SetsockoptInt(recvFd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, socketBufferSize)
	syscall.SetsockoptInt(sendFd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, socketBufferSize)

	if err := syscall.Bind(recvFd, &syscall.SockaddrInet4{Addr: [4]byte{0, 0, 0, 0}, Port: 0}); err != nil {
		syscall.Close(sendFd)
		syscall.Close(recvFd)
		return nil, fmt.Errorf("bind failed: %v", err)
	}

	return &unixRawSocket{sendFd: sendFd, recvFd: recvFd}, nil
}

func (s *unixRawSocket) recv(buf []byte) (int, error) {
	n, _, err := syscall.Recvfrom(s.recvFd, buf, 0)
	if err != nil {
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			return 0, errWouldBlock
		}
		return 0, err
	}
	return n, nil
}

func (s *unixRawSocket) send(packet []byte, dst [4]byte) error {
	return syscall.Sendto(s.sendFd, packet, 0, &syscall.SockaddrInet4{Addr: dst, Port: 0})
}

func (s *unixRawSocket) close() error {
	syscall.Close(s.sendFd)
	return syscall.Close(s.recvFd)
}
