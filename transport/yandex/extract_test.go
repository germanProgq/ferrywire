package yandex

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/germanProgq/ferrywire/transport"
)

func TestExtractBase64String(t *testing.T) {
	tr := &Transport{}

	cursor := `42["message",{"type":"cursor","cursor":"18;SGVsbG8="}]`
	if got := tr.extractBase64String(cursor); got != "SGVsbG8=" {
		t.Fatalf("cursor extract = %q", got)
	}

	save := `42["message",{"type":"saveChanges","excelAdditionalInfo":"QUJDMTIz"}]`
	if got := tr.extractBase64String(save); got != "QUJDMTIz" {
		t.Fatalf("saveChanges extract = %q", got)
	}

	if got := tr.extractBase64String(`42["message",{"type":"other"}]`); got != "" {
		t.Fatalf("message without cursor should extract nothing, got %q", got)
	}
}

func TestHandleMessageLegacyPacket(t *testing.T) {
	tr := newTestTransport(t)

	var got [][]byte
	tr.Receive(func(data []byte) { got = append(got, append([]byte{}, data...)) })

	payload := []byte{0x45, 0x00, 0x28, 0x99}
	tr.handleMessage(nil, []byte(cursorMessage(payload)))

	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatalf("legacy packet not delivered: %v", got)
	}
}

func TestHandleMessageBatchedPackets(t *testing.T) {
	tr := newTestTransport(t)

	var got [][]byte
	tr.Receive(func(data []byte) { got = append(got, append([]byte{}, data...)) })

	p1 := []byte{0x01, 0x02, 0x03}
	p2 := bytes.Repeat([]byte{0xEE}, 40)
	frame := encodeBatch([][]byte{p1, p2})
	tr.handleMessage(nil, []byte(cursorMessage(frame)))

	if len(got) != 2 || !bytes.Equal(got[0], p1) || !bytes.Equal(got[1], p2) {
		t.Fatalf("batched packets not delivered: %v", got)
	}
}

func newTestTransport(t *testing.T) *Transport {
	t.Helper()
	return NewTransport("", transport.DefaultConfig())
}

func cursorMessage(raw []byte) string {
	return fmt.Sprintf(`42["message",{"type":"cursor","cursor":"18;%s"}]`, base64.StdEncoding.EncodeToString(raw))
}
