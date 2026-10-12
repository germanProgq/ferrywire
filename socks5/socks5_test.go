package socks5

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseRequest(t *testing.T) {
	longDomain := strings.Repeat("a", 255)

	cases := []struct {
		name    string
		input   []byte
		want    string
		wantErr bool
	}{
		{
			name:  "ipv4",
			input: []byte{socksVersion, cmdConnect, 0x00, atypIPv4, 1, 2, 3, 4, 0x01, 0xBB},
			want:  "1.2.3.4:443",
		},
		{
			name: "domain",
			input: append(
				[]byte{socksVersion, cmdConnect, 0x00, atypDomain, 0x0B},
				append([]byte("example.com"), 0x00, 0x50)...,
			),
			want: "example.com:80",
		},
		{
			name: "max length domain",
			input: append(
				append([]byte{socksVersion, cmdConnect, 0x00, atypDomain, 0xFF}, []byte(longDomain)...),
				0x1F, 0x90,
			),
			want: longDomain + ":8080",
		},
		{
			name:    "bad version",
			input:   []byte{0x04, cmdConnect, 0x00, atypIPv4, 1, 2, 3, 4, 0x00, 0x50},
			wantErr: true,
		},
		{
			name:    "unsupported command",
			input:   []byte{socksVersion, 0x02, 0x00, atypIPv4, 1, 2, 3, 4, 0x00, 0x50},
			wantErr: true,
		},
		{
			name:    "unsupported address type",
			input:   []byte{socksVersion, cmdConnect, 0x00, 0x04},
			wantErr: true,
		},
		{
			name:    "truncated domain",
			input:   []byte{socksVersion, cmdConnect, 0x00, atypDomain, 0x0B, 'e', 'x'},
			wantErr: true,
		},
		{
			name:    "truncated header",
			input:   []byte{socksVersion, cmdConnect},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRequest(bytes.NewReader(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got addr %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNegotiateWritesMethodSelection(t *testing.T) {
	greeting := []byte{socksVersion, 0x01, methodNoAuth}
	request := []byte{socksVersion, cmdConnect, 0x00, atypIPv4, 10, 0, 0, 1, 0x01, 0xBB}

	conn := &scriptedConn{readBuf: bytes.NewBuffer(append(greeting, request...))}
	addr, err := negotiate(conn)
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	if addr != "10.0.0.1:443" {
		t.Fatalf("got %q", addr)
	}
	if !bytes.Equal(conn.writeBuf.Bytes(), []byte{socksVersion, methodNoAuth}) {
		t.Fatalf("method selection reply = %v", conn.writeBuf.Bytes())
	}
}

type scriptedConn struct {
	readBuf  *bytes.Buffer
	writeBuf bytes.Buffer
}

func (c *scriptedConn) Read(p []byte) (int, error)  { return c.readBuf.Read(p) }
func (c *scriptedConn) Write(p []byte) (int, error) { return c.writeBuf.Write(p) }
