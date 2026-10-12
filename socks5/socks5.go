package socks5

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

const (
	socksVersion = 0x05
	cmdConnect   = 0x01
	atypIPv4     = 0x01
	atypDomain   = 0x03
	methodNoAuth = 0x00
)

var (
	errUnsupportedVersion = errors.New("unsupported socks version")
	errUnsupportedCommand = errors.New("unsupported socks command")
	errUnsupportedAddress = errors.New("unsupported address type")
	errNoMethods          = errors.New("no authentication methods offered")
)

type Dialer interface {
	DialTCP(address string) (net.Conn, error)
}

type SOCKS5Server struct {
	listenAddr string
	dialer     Dialer
}

func NewSOCKS5Server(addr string, dialer Dialer) *SOCKS5Server {
	return &SOCKS5Server{listenAddr: addr, dialer: dialer}
}

func (s *SOCKS5Server) Start() error {
	listener, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("socks5 listen on %s: %w", s.listenAddr, err)
	}
	defer listener.Close()

	slog.Debug(fmt.Sprintf("[SOCKS5] Listening on %s", s.listenAddr))

	for {
		conn, err := listener.Accept()
		if err != nil {
			slog.Debug(fmt.Sprintf("[SOCKS5] Accept error: %v", err))
			continue
		}
		go s.handleConnection(conn)
	}
}

func (s *SOCKS5Server) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	targetAddr, err := negotiate(clientConn)
	if err != nil {
		slog.Debug(fmt.Sprintf("[SOCKS5] Handshake failed: %v", err))
		return
	}

	slog.Debug(fmt.Sprintf("[SOCKS5] CONNECT %s", targetAddr))

	targetConn, err := s.dialer.DialTCP(targetAddr)
	if err != nil {
		slog.Debug(fmt.Sprintf("[SOCKS5] Dial failed: %v", err))
		clientConn.Write([]byte{socksVersion, 0x04, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})
		return
	}
	defer targetConn.Close()

	clientConn.Write([]byte{socksVersion, 0x00, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})

	relay(clientConn, targetConn)
}

func negotiate(conn io.ReadWriter) (string, error) {
	if err := readGreeting(conn); err != nil {
		return "", err
	}
	if _, err := conn.Write([]byte{socksVersion, methodNoAuth}); err != nil {
		return "", err
	}
	return parseRequest(conn)
}

func readGreeting(r io.Reader) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return errUnsupportedVersion
	}
	methodCount := int(header[1])
	if methodCount == 0 {
		return errNoMethods
	}
	methods := make([]byte, methodCount)
	_, err := io.ReadFull(r, methods)
	return err
}

func parseRequest(r io.Reader) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return "", err
	}
	if header[0] != socksVersion {
		return "", errUnsupportedVersion
	}
	if header[1] != cmdConnect {
		return "", errUnsupportedCommand
	}

	switch header[3] {
	case atypIPv4:
		buf := make([]byte, 4+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d.%d.%d.%d:%d",
			buf[0], buf[1], buf[2], buf[3],
			uint16(buf[4])<<8|uint16(buf[5])), nil
	case atypDomain:
		lengthByte := make([]byte, 1)
		if _, err := io.ReadFull(r, lengthByte); err != nil {
			return "", err
		}
		domainLen := int(lengthByte[0])
		buf := make([]byte, domainLen+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s:%d",
			string(buf[:domainLen]),
			uint16(buf[domainLen])<<8|uint16(buf[domainLen+1])), nil
	default:
		return "", errUnsupportedAddress
	}
}

func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer b.Close()
		io.Copy(b, a)
	}()

	go func() {
		defer wg.Done()
		defer a.Close()
		io.Copy(a, b)
	}()

	wg.Wait()
}
