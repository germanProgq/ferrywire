package yandex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/germanProgq/ferrywire/transport"
)

const (
	batchMarker   = 0x01
	lenPrefixSize = 3
	maxBatchBytes = 4096
	maxBatchCount = 128
)

var (
	clientConfigRe = regexp.MustCompile(`<script[^>]*id="client-config"[^>]*>(.*?)</script>`)
	cursorRe       = regexp.MustCompile(`"cursor":"[^;]+;([^"]+)"`)
)

func encodeBatch(packets [][]byte) []byte {
	total := 1
	for _, p := range packets {
		total += lenPrefixSize + len(p)
	}
	buf := make([]byte, 0, total)
	buf = append(buf, batchMarker)
	for _, p := range packets {
		n := len(p)
		buf = append(buf, byte(n>>16), byte(n>>8), byte(n))
		buf = append(buf, p...)
	}
	return buf
}

func decodeBatch(buf []byte) [][]byte {
	var packets [][]byte
	i := 1
	for i+lenPrefixSize <= len(buf) {
		n := int(buf[i])<<16 | int(buf[i+1])<<8 | int(buf[i+2])
		i += lenPrefixSize
		if n <= 0 || i+n > len(buf) {
			break
		}
		packets = append(packets, buf[i:i+n])
		i += n
	}
	return packets
}

func drainBatch(q chan []byte, first []byte) [][]byte {
	packets := [][]byte{first}
	total := lenPrefixSize + len(first)
	for len(packets) < maxBatchCount && total < maxBatchBytes {
		select {
		case p := <-q:
			packets = append(packets, p)
			total += lenPrefixSize + len(p)
		default:
			return packets
		}
	}
	return packets
}

type DocsInfo struct {
	CookieStr   string
	Token       string
	DocID       string
	CallbackURL string
	UserID      string
	Origin      string
	Host        string
	WsURL       string
	Permissions map[string]interface{}
	OpenCmd     map[string]interface{}
}

type Session struct {
	Info       DocsInfo
	Conn       *websocket.Conn
	WriteQueue chan []byte
	UserID     string
	writeMu    sync.Mutex
}

func (s *Session) safeWrite(messageType int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.Conn.WriteMessage(messageType, data)
}

type Transport struct {
	*transport.Base

	url       string
	session   *Session
	sessionMu sync.RWMutex

	userCounter atomic.Int32
	baseUserID  string
}

func NewTransport(url string, config transport.Config) *Transport {
	t := &Transport{
		Base: transport.NewBase(config),
		url:  url,
	}
	t.baseUserID = randUserID()
	return t
}

func (t *Transport) Start() error {
	if err := t.Base.Start(); err != nil {
		return err
	}

	t.baseUserID = randUserID()
	go t.keepAliveLoop()
	t.connectToDoc(0)

	return nil
}

func (t *Transport) Stop() {
	t.Base.Stop()
	t.SetConnected(false)
	t.sessionMu.RLock()
	session := t.session
	t.sessionMu.RUnlock()
	if session != nil && session.Conn != nil {
		session.Conn.Close()
	}
}

func (t *Transport) Send(data []byte) error {
	if !t.IsConnected() {
		return fmt.Errorf("transport not connected")
	}

	t.sessionMu.RLock()
	session := t.session
	t.sessionMu.RUnlock()

	if session == nil {
		return fmt.Errorf("no active session")
	}

	select {
	case session.WriteQueue <- data:
		return nil
	default:
		return fmt.Errorf("write queue full")
	}
}

func (t *Transport) connectToDoc(attempt int) {
	if !t.IsRunning() {
		return
	}

	slog.Debug("[YDOCS] connectToDoc attempt ...")

	go func() {
		t.sessionMu.Lock()
		existingSession := t.session
		t.sessionMu.Unlock()

		var userID string
		if existingSession != nil {
			userID = existingSession.UserID
		} else {
			suffix := fmt.Sprintf("%03d", t.userCounter.Add(1)%1000)
			userID = t.baseUserID + suffix
		}

		info, err := t.fetchDocInfo(t.url, userID)
		if err != nil {
			slog.Debug(fmt.Sprintf("[YDOCS] fetchDocInfo failed: %v", err))
			t.scheduleReconnect(attempt)
			return
		}

		dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
		headers := http.Header{}
		headers.Set("User-Agent", "Mozilla/5.0")
		headers.Set("Origin", info.Origin)
		headers.Set("Cookie", info.CookieStr)
		headers.Set("Host", info.Host)

		conn, _, err := dialer.Dial(info.WsURL, headers)
		if err != nil {
			slog.Debug(fmt.Sprintf("[YDOCS] WebSocket dial failed: %v", err))
			t.scheduleReconnect(attempt)
			return
		}

		if existingSession != nil && existingSession.Conn != nil {
			existingSession.Conn.Close()
		}

		writeQueue := make(chan []byte, t.GetConfig().MaxQueueSize)
		if existingSession != nil {
			writeQueue = existingSession.WriteQueue
		}

		session := &Session{
			Info:       info,
			Conn:       conn,
			WriteQueue: writeQueue,
			UserID:     userID,
		}

		t.sessionMu.Lock()
		t.session = session
		t.SetConnected(true)
		t.sessionMu.Unlock()

		if existingSession == nil {
			go t.writerLoop()
		}

		auth1 := fmt.Sprintf(`40{"token":"%s"}`, info.Token)
		session.safeWrite(websocket.TextMessage, []byte(auth1))

		authData := map[string]interface{}{
			"type": "auth", "docid": info.DocID, "token": "fghhfgsjdgfjs",
			"user": map[string]interface{}{"id": userID}, "editorType": 0,
			"lastOtherSaveTime": -1, "permissions": info.Permissions,
			"openCmd": info.OpenCmd, "coEditingMode": "fast", "jwtOpen": info.Token,
		}
		messagePart, _ := json.Marshal([]interface{}{"message", authData})
		session.safeWrite(websocket.TextMessage, []byte(fmt.Sprintf("42%s", string(messagePart))))

		for t.IsRunning() {
			_, message, err := conn.ReadMessage()
			if err != nil {
				slog.Debug(fmt.Sprintf("[YDOCS] Read error: %v", err))
				t.SetConnected(false)
				conn.Close()
				t.scheduleReconnect(attempt)
				return
			}
			t.handleMessage(session, message)
		}
	}()
}

func (t *Transport) writerLoop() {
	for t.IsRunning() {
		t.sessionMu.Lock()
		session := t.session
		t.sessionMu.Unlock()

		if session == nil || session.Conn == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		select {
		case first := <-session.WriteQueue:
			packets := drainBatch(session.WriteQueue, first)
			payload := base64.StdEncoding.EncodeToString(encodeBatch(packets))
			msg := fmt.Sprintf(`42["message",{"type":"cursor","cursor":"18;%s"}]`, payload)

			if err := session.safeWrite(websocket.TextMessage, []byte(msg)); err != nil {
				slog.Debug(fmt.Sprintf("[YDOCS] Write error: %v", err))
			}
		case <-time.After(time.Second):
		}
	}
}

func (t *Transport) keepAliveLoop() {
	ticker := time.NewTicker(t.GetConfig().KeepAliveInterval)
	defer ticker.Stop()
	keepAliveMsg := `42["message",{"type":"cursor","cursor":"18;---KA---"}]`

	for t.IsRunning() {
		<-ticker.C
		t.sessionMu.Lock()
		session := t.session
		t.sessionMu.Unlock()

		if session != nil && session.Conn != nil {
			if err := session.safeWrite(websocket.TextMessage, []byte(keepAliveMsg)); err != nil {
				slog.Debug(fmt.Sprintf("[YDOCS] Keep-alive failed: %v", err))
				t.SetConnected(false)
			}
		}
	}
}

func (t *Transport) handleMessage(session *Session, data []byte) {
	text := string(data)

	if strings.Contains(text, "---KA---") {
		return
	}

	if text == "2" {
		if session != nil && session.Conn != nil {
			session.safeWrite(websocket.TextMessage, []byte("3"))
		}
		return
	}
	if text == "3" {
		return
	}

	if strings.Contains(text, "saveChanges") || strings.Contains(text, "cursor") {
		base64Str := t.extractBase64String(text)
		if base64Str == "" {
			return
		}

		decoded, err := base64.StdEncoding.DecodeString(base64Str)
		if err != nil {
			slog.Debug(fmt.Sprintf("[YDOCS] Base64 decode error: %v", err))
			return
		}

		if len(decoded) > 0 && decoded[0] == batchMarker {
			for _, pkt := range decodeBatch(decoded) {
				t.CallReceive(pkt)
			}
			return
		}

		t.CallReceive(decoded)
	}
}

func (t *Transport) extractBase64String(response string) string {
	if strings.Contains(response, "saveChanges") {
		marker := `"excelAdditionalInfo":"`
		left := strings.Index(response, marker) + len(marker)
		if left < len(marker) {
			return ""
		}
		right := strings.Index(response[left:], `"`)
		if right == -1 {
			return ""
		}
		return response[left : left+right]
	}

	matches := cursorRe.FindStringSubmatch(response)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func (t *Transport) scheduleReconnect(attempt int) {
	if !t.IsRunning() {
		return
	}
	if attempt >= t.GetConfig().MaxReconnectAttempts {
		t.ReportFatal(fmt.Errorf("yandex docs: gave up after %d reconnect attempts", attempt))
		return
	}

	t.connectToDoc(attempt + 1)
}

func (t *Transport) fetchDocInfo(url, userID string) (DocsInfo, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return nil },
		Timeout:       30 * time.Second,
	}

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return DocsInfo{}, err
	}
	defer resp.Body.Close()

	htmlBytes, _ := io.ReadAll(resp.Body)
	html := string(htmlBytes)

	var cookies []string
	for _, c := range resp.Cookies() {
		cookies = append(cookies, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}

	matches := clientConfigRe.FindStringSubmatch(html)
	if len(matches) < 2 {
		return DocsInfo{}, fmt.Errorf("config not found")
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(matches[1]), &config); err != nil {
		return DocsInfo{}, fmt.Errorf("parse client-config: %w", err)
	}

	officeAction, ok := mapField(config, "officeActionData")
	if !ok {
		return DocsInfo{}, fmt.Errorf("officeActionData missing")
	}
	editorConfig, ok := mapField(officeAction, "editor_config")
	if !ok {
		return DocsInfo{}, fmt.Errorf("editor_config nil - will reconnect")
	}
	balancerURL, ok := stringField(officeAction, "balancer_url")
	if !ok {
		return DocsInfo{}, fmt.Errorf("balancer_url missing")
	}
	document, ok := mapField(editorConfig, "document")
	if !ok {
		return DocsInfo{}, fmt.Errorf("document missing")
	}
	token, ok := stringField(editorConfig, "token")
	if !ok {
		return DocsInfo{}, fmt.Errorf("token missing")
	}
	docKey, ok := stringField(document, "key")
	if !ok {
		return DocsInfo{}, fmt.Errorf("document key missing")
	}

	host := strings.TrimPrefix(balancerURL, "https://")
	perms, _ := document["permissions"].(map[string]interface{})
	if perms == nil {
		perms = make(map[string]interface{})
	}

	return DocsInfo{
		CookieStr:   strings.Join(cookies, "; "),
		Token:       token,
		DocID:       docKey,
		Origin:      balancerURL,
		Host:        host,
		WsURL:       fmt.Sprintf("wss://%s/2024.1.1-375/doc/%s/c/?EIO=4&transport=websocket", host, docKey),
		Permissions: perms,
		OpenCmd: map[string]interface{}{
			"c":      "open",
			"id":     docKey,
			"userid": userID,
			"format": document["fileType"],
			"url":    document["url"],
			"title":  document["title"],
			"lcid":   25,
		},
	}, nil
}

func mapField(m map[string]interface{}, key string) (map[string]interface{}, bool) {
	v, ok := m[key].(map[string]interface{})
	return v, ok
}

func stringField(m map[string]interface{}, key string) (string, bool) {
	v, ok := m[key].(string)
	return v, ok
}

func randUserID() string {
	return fmt.Sprintf("%010d", rand.New(rand.NewSource(time.Now().UnixNano())).Intn(1000000000))
}
