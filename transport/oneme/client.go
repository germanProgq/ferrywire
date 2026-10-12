package oneme

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsHost     = "wss://ws-api.oneme.ru/websocket"
	rpcVersion = 11
	userAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"
)

const (
	opcodeKeepAlive    = 1
	opcodeSessionInit  = 6
	opcodeLogin        = 19
	opcodeStartCall    = 78
	opcodeIncomingCall = 137
)

func NewClient() *Client {
	return &Client{
		deviceID:      genUUID(),
		keepaliveStop: make(chan struct{}),
		done:          make(chan struct{}),
	}
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	header := http.Header{}
	header.Set("Origin", "https://web.max.ru")
	header.Set("User-Agent", userAgent)
	conn, _, err := websocket.DefaultDialer.Dial(wsHost, header)
	if err != nil {
		return err
	}
	c.conn = conn
	go c.readLoop()
	slog.Debug("[MAX] Connected")
	return nil
}

func (c *Client) SetEventCallback(cb func(Packet)) { c.onEvent = cb }

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.keepaliveStop)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.conn != nil {
			c.conn.Close()
		}
	})
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var packet Packet
		if json.Unmarshal(message, &packet) != nil {
			continue
		}
		if ch, ok := c.pending.LoadAndDelete(int64(packet.Seq)); ok {
			ch.(chan Packet) <- packet
		} else if c.onEvent != nil {
			c.onEvent(packet)
		}
	}
}

func (c *Client) invoke(opcode int, payload map[string]interface{}) (*Packet, error) {
	seq := c.seq.Add(1)
	req := map[string]interface{}{"ver": rpcVersion, "cmd": 0, "seq": seq, "opcode": opcode, "payload": payload}
	data, _ := json.Marshal(req)
	ch := make(chan Packet, 1)
	c.pending.Store(seq, ch)
	defer c.pending.Delete(seq)
	c.mu.Lock()
	err := c.conn.WriteMessage(websocket.TextMessage, data)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		return &resp, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout")
	}
}

func (c *Client) LoginByToken(token string) error {
	_, err := c.invoke(opcodeSessionInit, map[string]interface{}{
		"userAgent": map[string]interface{}{
			"deviceType": "WEB", "locale": "ru_RU", "osVersion": "macOS",
			"deviceName": "vkmax Go", "appVersion": "25.9.15",
			"screen": "956x1470 2.0x", "timezone": "Asia/Vladivostok",
		},
		"deviceId": c.deviceID,
	})
	if err != nil {
		return fmt.Errorf("session init: %w", err)
	}
	resp, err := c.invoke(opcodeLogin, map[string]interface{}{
		"interactive": true, "token": token, "chatsSync": 0,
		"contactsSync": 0, "presenceSync": 0, "draftsSync": 0, "chatsCount": 40,
	})
	if err != nil {
		return err
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		return fmt.Errorf("login response: %w", err)
	}
	if _, ok := payload["error"]; ok {
		return fmt.Errorf("login failed: %v", payload["error"])
	}
	c.loggedIn = true
	go c.keepalive()
	return nil
}

func (c *Client) keepalive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.keepaliveStop:
			return
		case <-ticker.C:
			if c.loggedIn {
				c.invoke(opcodeKeepAlive, map[string]interface{}{"interactive": false})
			}
		}
	}
}
