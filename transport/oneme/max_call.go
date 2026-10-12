package oneme

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

var (
	useICEInjection = true
)

func (h *CallHandler) Send(data []byte) {
	if useICEInjection {
		h.injectICE(data)
	} else {
		if h.dc != nil {
			h.dc.Send(data)
		}
	}
}

func (h *CallHandler) readLoop() {
	slog.Debug(fmt.Sprintf("[%s] Signaling connected", h.tag))
	for {
		_, message, err := h.conn.ReadMessage()
		if err != nil {
			slog.Error(fmt.Sprintf("[%s] Signaling disconnected: %v", h.tag, err))
			h.signalReconnect()
			return
		}
		text := string(message)

		if strings.Contains(text, "accepted-call") {
			slog.Debug("call accepted")
			h.callAccepted = true
			continue
		}

		if text == "ping" {
			h.mu.Lock()
			if h.conn != nil {
				h.conn.WriteMessage(websocket.TextMessage, []byte("pong"))
			}
			h.mu.Unlock()
			continue
		}
		if len(text) < 10 {
			continue
		}
		var data map[string]interface{}
		if json.Unmarshal([]byte(text), &data) != nil {
			continue
		}
		if t, _ := data["type"].(string); t == "response" {
			continue
		}
		if t, _ := data["type"].(string); t == "error" {
			slog.Error(fmt.Sprintf("[%s] SIGNALING ERROR: %v", h.tag, data["message"]))
			continue
		}

		if pid, ok := data["participantId"].(float64); ok {
			h.remoteID = int64(pid)
		}
		go h.msgHandler(text)
	}
}

func (h *CallHandler) signalReconnect() {
	if h.role == "caller" {
		select {
		case h.reconnectCh <- struct{}{}:
		default:
		}
		return
	}
	slog.Error(fmt.Sprintf("[%s] Receiver connection died", h.tag))
}

func (h *CallHandler) sendAcceptCall() {
	if h.acceptSent {
		return
	}
	h.acceptSent = true
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return
	}
	msg := fmt.Sprintf(`{"command":"accept-call","sequence":%d,"mediaSettings":{"isAudioEnabled":true,"isVideoEnabled":false,"isScreenSharingEnabled":false,"isFastScreenSharingEnabled":false,"isAudioSharingEnabled":false,"isAnimojiEnabled":false}}`, h.seq)
	h.seq++
	h.conn.WriteMessage(websocket.TextMessage, []byte(msg))
	slog.Debug(fmt.Sprintf("[%s] Accept-call sent", h.tag))
}

func (h *CallHandler) createPeerConnection(convParams map[string]interface{}) {
	slog.Debug(fmt.Sprintf("[%s] Creating PeerConnection...", h.tag))
	turn, ok := convParams["turn"].(map[string]interface{})
	if !ok {
		slog.Error(fmt.Sprintf("[%s] conversationParams missing turn", h.tag))
		return
	}
	stun, ok := convParams["stun"].(map[string]interface{})
	if !ok {
		slog.Error(fmt.Sprintf("[%s] conversationParams missing stun", h.tag))
		return
	}
	var stunURLs, turnURLs []string
	if urls, ok := stun["urls"].([]interface{}); ok && len(urls) > 0 {
		stunURLs = []string{urls[0].(string)}
	}
	if urls, ok := turn["urls"].([]interface{}); ok {
		for _, u := range urls {
			turnURLs = append(turnURLs, u.(string))
		}
	}
	username, _ := turn["username"].(string)
	credential, _ := turn["credential"].(string)
	slog.Debug(fmt.Sprintf("[%s] STUN: %v  TURN: %v", h.tag, stunURLs, turnURLs))

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs:           turnURLs,
				Username:       username,
				Credential:     credential,
				CredentialType: webrtc.ICECredentialTypePassword,
			},
		},
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		slog.Error(fmt.Sprintf("[%s] ERROR creating PC: %v", h.tag, err))
		return
	}
	h.pc = pc

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			jsonC, _ := json.Marshal(c.ToJSON())
			slog.Debug(fmt.Sprintf("[%s] Local ICE: %s", h.tag, string(jsonC)))
			h.sendICE(string(jsonC))
		} else {
			slog.Debug(fmt.Sprintf("[%s] ICE gathering complete", h.tag))
		}
	})
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		slog.Debug(fmt.Sprintf("[%s] ICE: %s", h.tag, s.String()))
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		slog.Debug(fmt.Sprintf("[%s] Connection: %s", h.tag, s.String()))
		if s == webrtc.PeerConnectionStateConnected {
			slog.Debug(fmt.Sprintf("[%s] *** CONNECTED! ***", h.tag))
		}
	})
	pc.OnSignalingStateChange(func(s webrtc.SignalingState) {
		slog.Debug(fmt.Sprintf("[%s] Signaling: %s", h.tag, s.String()))
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dcID := uint16(0)
		if dc.ID() != nil {
			dcID = *dc.ID()
		}
		slog.Debug(fmt.Sprintf("[%s] Remote DC: %s (id=%d)", h.tag, dc.Label(), dcID))
		h.dc = dc
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			slog.Debug(fmt.Sprintf("[%s] RECV: %s", h.tag, string(msg.Data)))
			h.dcInbound(msg.Data)
		})
	})

	ordered := true
	maxRetransmits := uint16(0)
	dc, err := pc.CreateDataChannel("x", &webrtc.DataChannelInit{
		Ordered: &ordered, MaxRetransmits: &maxRetransmits,
	})
	if err != nil {
		slog.Error(fmt.Sprintf("[%s] ERROR creating DC: %v", h.tag, err))
		return
	}
	if dc == nil {
		slog.Error(fmt.Sprintf("[%s] DC is nil", h.tag))
		return
	}
	h.dc = dc
	dc.OnOpen(func() { slog.Debug(fmt.Sprintf("[%s] DC opened", h.tag)) })
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		h.dcInbound(msg.Data)
	})
}

func (h *CallHandler) sendSDP(sdp string, sdpType string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return
	}
	escaped, _ := json.Marshal(sdp)
	msg := fmt.Sprintf(`{"command":"transmit-data","sequence":%d,"participantId":%d,"data":{"sdp":{"type":"%s","sdp":%s},"animojiVersion":1},"participantType":"USER"}`,
		h.seq, h.localID, sdpType, string(escaped))
	h.seq++
	slog.Debug(fmt.Sprintf("[%s] Sent SDP %s (%d bytes)", h.tag, sdpType, len(sdp)))
	slog.Debug(msg)
}

func (h *CallHandler) injectICE(payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return
	}
	type ice struct {
		Candidate string `json:"candidate"`
	}
	structPayload := ice{Candidate: base64.StdEncoding.EncodeToString(payload)}
	escaped, _ := json.Marshal(structPayload.Candidate)
	msg := fmt.Sprintf(`{"command":"transmit-data","sequence":%d,"participantId":%d,"data":{"candidate":{"candidate":%s}},"participantType":"USER"}`,
		h.seq, h.localID, string(escaped))
	h.seq++
	h.conn.WriteMessage(websocket.TextMessage, []byte(msg))
}

func (h *CallHandler) sendICE(candidateJSON string) {
	if useICEInjection {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return
	}
	var ice struct {
		Candidate string `json:"candidate"`
	}
	json.Unmarshal([]byte(candidateJSON), &ice)
	escaped, _ := json.Marshal(ice.Candidate)
	msg := fmt.Sprintf(`{"command":"transmit-data","sequence":%d,"participantId":%d,"data":{"candidate":{"candidate":%s}},"participantType":"USER"}`,
		h.seq, h.localID, string(escaped))
	h.seq++
	h.conn.WriteMessage(websocket.TextMessage, []byte(msg))
}

func (h *CallHandler) addICECandidate(c map[string]interface{}) {
	if h.pc == nil {
		return
	}
	candidateStr, _ := c["candidate"].(string)
	sdpMid, _ := c["sdpMid"].(string)
	sdpMLineIndex := uint16(0)
	if idx, ok := c["sdpMLineIndex"].(float64); ok {
		sdpMLineIndex = uint16(idx)
	}
	if err := h.pc.AddICECandidate(webrtc.ICECandidateInit{
		Candidate: candidateStr, SDPMid: &sdpMid, SDPMLineIndex: &sdpMLineIndex,
	}); err != nil {
		slog.Error(fmt.Sprintf("[%s] ICE error: %v", h.tag, err))
	}
}

func (h *CallHandler) bufferOrAddICE(c map[string]interface{}) {
	if !h.hasRemoteDesc {
		h.pendingCandidates = append(h.pendingCandidates, c)
		slog.Debug(fmt.Sprintf("[%s] Buffered ICE (%d total)", h.tag, len(h.pendingCandidates)))
	} else {
		h.addICECandidate(c)
	}
}

func (h *CallHandler) flushPendingCandidates() {
	if len(h.pendingCandidates) == 0 {
		return
	}
	slog.Debug(fmt.Sprintf("[%s] Flushing %d buffered ICE", h.tag, len(h.pendingCandidates)))
	for _, c := range h.pendingCandidates {
		h.addICECandidate(c)
	}
	h.pendingCandidates = nil
}

func (h *CallHandler) handleSDP(sdpType string, sdpStr string) {
	if h.pc == nil {
		return
	}
	slog.Debug(fmt.Sprintf("[%s] handleSDP: %s (%d bytes)", h.tag, sdpType, len(sdpStr)))

	switch sdpType {
	case "offer":
		slog.Debug(fmt.Sprintf("[%s] Setting remote offer...", h.tag))
		if err := h.pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer, SDP: sdpStr,
		}); err != nil {
			slog.Error(fmt.Sprintf("[%s] ERROR: %v", h.tag, err))
			return
		}
		time.Sleep(300 * time.Millisecond)
		h.hasRemoteDesc = true
		h.flushPendingCandidates()

		slog.Debug(fmt.Sprintf("[%s] Creating answer...", h.tag))
		answer, err := h.pc.CreateAnswer(nil)
		if err != nil {
			slog.Error(fmt.Sprintf("[%s] ERROR: %v", h.tag, err))
			return
		}
		h.sendSDP(answer.SDP, "answer")

	case "answer":
		slog.Debug(fmt.Sprintf("[%s] Setting remote answer...", h.tag))
		if err := h.pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeAnswer, SDP: sdpStr,
		}); err != nil {
			slog.Error(fmt.Sprintf("[%s] ERROR: %v", h.tag, err))
			return
		}
		h.hasRemoteDesc = true
		h.flushPendingCandidates()
	}
}

func localParticipantID(data map[string]interface{}, wantCreator bool) (int64, bool) {
	conv, ok := data["conversation"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	parts, ok := conv["participants"].([]interface{})
	if !ok {
		return 0, false
	}

	var id int64
	found := false
	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		isCreator := false
		if roles, ok := part["roles"].([]interface{}); ok {
			for _, r := range roles {
				if s, ok := r.(string); ok && s == "CREATOR" {
					isCreator = true
				}
			}
		}
		if isCreator != wantCreator {
			continue
		}
		if fid, ok := part["id"].(float64); ok {
			id = int64(fid)
			found = true
		}
	}
	return id, found
}

func startOutgoingCall(client *MaxClient, calleeID int64) *CallHandler {
	h := &CallHandler{tag: "CALLER", role: "caller"}
	h.seq = 1
	h.reconnectCh = make(chan struct{}, 1)
	h.msgHandler = func(text string) {
		var data map[string]interface{}
		json.Unmarshal([]byte(text), &data)

		if h.localID == 0 {
			if id, ok := localParticipantID(data, false); ok {
				h.localID = id
				slog.Debug(fmt.Sprintf("[%s] Local ID: %d", h.tag, h.localID))
			}
		}

		if cp, ok := data["conversationParams"].(map[string]interface{}); ok {
			slog.Debug(fmt.Sprintf("[%s] conversationParams - creating offer", h.tag))
			for {
				time.Sleep(1 * time.Second)
				slog.Debug("waiting for accept ...")
				if h.callAccepted || useICEInjection {
					break
				}
			}
			h.createPeerConnection(cp)
			time.Sleep(200 * time.Millisecond)
			offer, err := h.pc.CreateOffer(nil)
			if err != nil {
				slog.Error(fmt.Sprintf("[%s] ERROR: %v", h.tag, err))
				return
			}
			h.sendSDP(offer.SDP, "offer")
			return
		}
		d, _ := data["data"].(map[string]interface{})
		if d == nil {
			return
		}
		if sdp, ok := d["sdp"].(map[string]interface{}); ok {
			sdpType, _ := sdp["type"].(string)
			if sdpType == "answer" {
				if useICEInjection {
					return
				}
				sdpStr, _ := sdp["sdp"].(string)
				h.handleSDP(sdpType, sdpStr)
			}
			return
		}
		if c, ok := d["candidate"].(map[string]interface{}); ok {
			if useICEInjection {
				candidateStr, _ := c["candidate"].(string)
				decode, _ := base64.StdEncoding.DecodeString(candidateStr)
				h.dcInbound(decode)
			} else {
				h.bufferOrAddICE(c)
			}
		}
	}

	slog.Debug(fmt.Sprintf("[CALLER] Calling %d", calleeID))

	go func() {
		for {
			h.mu.Lock()
			h.callAccepted = false
			h.acceptSent = false
			h.hasRemoteDesc = false
			h.pendingCandidates = nil
			h.seq = 1
			if h.pc != nil {
				h.pc.Close()
				h.pc = nil
			}
			h.dc = nil
			h.mu.Unlock()

			resp, err := client.invoke(opcodeStartCall, map[string]interface{}{
				"conversationId": genUUID(),
				"calleeIds":      []int64{calleeID},
				"internalParams": fmt.Sprintf(`{"deviceId":"%s","sdkVersion":"2.8.9","clientAppKey":"CNHIJPLGDIHBABABA","platform":"WEB","protocolVersion":5,"domainId":"","capabilities":"2A03F"}`, client.deviceID),
				"isVideo":        false,
			})
			if err != nil || resp == nil {
				slog.Error(fmt.Sprintf("[CALLER] start-call invoke failed: %v", err))
				time.Sleep(1 * time.Second)
				continue
			}
			var payload map[string]interface{}
			json.Unmarshal(resp.Payload, &payload)
			paramsStr, _ := payload["internalCallerParams"].(string)
			var params InternalCallerParams
			json.Unmarshal([]byte(paramsStr), &params)

			endpoint := params.Endpoint + "&platform=WEB&appVersion=1.1&version=5&device=browser&capabilities=2A03F&clientType=ONE_ME&tgt=start"
			conn, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
			if err != nil {
				slog.Error(fmt.Sprintf("[CALLER] Dial error: %v, retrying...", err))
				time.Sleep(1 * time.Second)
				continue
			}
			h.mu.Lock()
			h.conn = conn
			h.mu.Unlock()
			go h.readLoop()

			<-h.reconnectCh
			slog.Debug("[CALLER] Reconnecting in 1s...")
			time.Sleep(1 * time.Second)
		}
	}()

	return h
}

func startIncomingListener(client *MaxClient) *CallHandler {
	h := &CallHandler{tag: "RECEIVER", role: "receiver"}
	h.seq = 1
	h.msgHandler = func(text string) {
		var data map[string]interface{}
		json.Unmarshal([]byte(text), &data)

		if h.localID == 0 {
			if id, ok := localParticipantID(data, true); ok {
				h.localID = id
				slog.Debug(fmt.Sprintf("[%s] Local ID: %d", h.tag, h.localID))
			}
		}

		if cp, ok := data["conversationParams"].(map[string]interface{}); ok {
			h.createPeerConnection(cp)
			return
		}
		d, _ := data["data"].(map[string]interface{})
		if d == nil {
			return
		}
		if sdp, ok := d["sdp"].(map[string]interface{}); ok {
			sdpType, _ := sdp["type"].(string)
			sdpStr, _ := sdp["sdp"].(string)
			h.handleSDP(sdpType, sdpStr)
			return
		}
		if c, ok := d["candidate"].(map[string]interface{}); ok {
			if useICEInjection {
				candidateStr, _ := c["candidate"].(string)
				decode, _ := base64.StdEncoding.DecodeString(candidateStr)
				h.dcInbound(decode)
			} else {
				h.bufferOrAddICE(c)
			}
		}
	}

	client.SetEventCallback(func(p MaxPacket) {
		if p.Opcode == opcodeIncomingCall {
			slog.Debug("[RECEIVER] Incoming call!")
			var payload map[string]interface{}
			json.Unmarshal(p.Payload, &payload)
			convID, _ := payload["conversationId"].(string)
			vcp, _ := payload["vcp"].(string)
			callDetails, err := decodeCallDetails(vcp)
			if err != nil {
				slog.Error(fmt.Sprintf("[RECEIVER] Decode error: %v", err))
				return
			}

			endpoint := craftEndpoint(convID, callDetails)
			slog.Debug(fmt.Sprintf("[RECEIVER] Initial endpoint: %s", endpoint))

			conn, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
			if err != nil {
				slog.Error(fmt.Sprintf("[RECEIVER] Connect error: %v", err))
				return
			}
			h.mu.Lock()
			if h.conn != nil {
				h.conn.Close()
			}
			h.conn = conn
			h.mu.Unlock()
			go h.readLoop()
			go func() {
				time.Sleep(1 * time.Second)
				if !h.acceptSent {
					h.sendAcceptCall()
				}
			}()
		}
	})
	slog.Debug("[RECEIVER] Waiting for calls...")
	return h
}
