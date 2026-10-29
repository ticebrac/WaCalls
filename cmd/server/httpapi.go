package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"wacalls/internal/voip/core"

	"go.mau.fi/whatsmeow/types"
)

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/sessions", s.handleSessionList)
	mux.HandleFunc("POST /api/sessions", s.handleSessionCreate)
	mux.HandleFunc("DELETE /api/sessions/{sid}", s.handleSessionDelete)
	mux.HandleFunc("POST /api/sessions/{sid}/logout", s.handleSessionLogout)
	mux.HandleFunc("POST /api/sessions/{sid}/pair", s.handleSessionPair)
	mux.HandleFunc("POST /api/sessions/{sid}/calls", s.handleStartCall)
	mux.HandleFunc("POST /api/sessions/{sid}/calls/{id}/webrtc", s.handleWebRTC)
	mux.HandleFunc("POST /api/sessions/{sid}/calls/{id}/accept", s.handleAccept)
	mux.HandleFunc("POST /api/sessions/{sid}/calls/{id}/reject", s.handleReject)
	mux.HandleFunc("DELETE /api/sessions/{sid}/calls/{id}", s.handleEndCall)
	mux.HandleFunc("GET /api/sessions/{sid}/history", s.handleHistory)

	mux.HandleFunc("GET /api/sessions/{sid}/chats", s.handleChatList)
	mux.HandleFunc("GET /api/sessions/{sid}/messages", s.handleMessageList)
	mux.HandleFunc("POST /api/sessions/{sid}/messages", s.handleSendMessage)
	mux.HandleFunc("POST /api/sessions/{sid}/messages/media", s.handleSendMedia)
	mux.HandleFunc("POST /api/sessions/{sid}/chats/read", s.handleMarkRead)
	mux.HandleFunc("POST /api/sessions/{sid}/presence", s.handleSetPresence)
	mux.HandleFunc("POST /api/sessions/{sid}/check-number", s.handleCheckNumber)
	mux.HandleFunc("POST /api/sessions/{sid}/profile-picture", s.handleProfilePicture)
	mux.HandleFunc("GET /api/media/{sid}/{file}", s.handleMediaFile)

	mux.HandleFunc("GET /api/events", s.handleEvents)

	s.evoRoutes(mux)

	if s.staticDir != "" {
		if _, err := os.Stat(s.staticDir); err == nil {
			mux.Handle("/", http.FileServer(http.Dir(s.staticDir)))
		}
	}
	return withCORS(mux)
}

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Client-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func clientID(r *http.Request) string {
	if id := r.Header.Get("X-Client-Id"); id != "" {
		return id
	}
	return r.URL.Query().Get("clientId")
}

func (s *server) sessionByID(w http.ResponseWriter, sid string) *Session {
	sess, ok := s.sessions.Get(sid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such session"})
		return nil
	}
	return sess
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.broker.serveSSE(w, r, clientID(r))
}

func (s *server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.sessions.infos()})
}

func (s *server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Session"
	}
	id, err := s.sessions.Create(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Delete(r.Context(), r.PathValue("sid")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSessionLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Logout(r.Context(), r.PathValue("sid")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSessionPair(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Pair(r.PathValue("sid")); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleStartCall(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionByID(w, r.PathValue("sid")); sess != nil {
		s.doStartCall(sess, w, r)
	}
}

func (s *server) handleWebRTC(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionByID(w, r.PathValue("sid")); sess != nil {
		s.doWebRTC(sess, w, r)
	}
}

func (s *server) handleAccept(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionByID(w, r.PathValue("sid")); sess != nil {
		s.doAccept(sess, w, r)
	}
}

func (s *server) handleReject(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionByID(w, r.PathValue("sid")); sess != nil {
		s.doReject(sess, w, r)
	}
}

func (s *server) handleEndCall(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionByID(w, r.PathValue("sid")); sess != nil {
		s.doEndCall(sess, w, r)
	}
}

func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionByID(w, r.PathValue("sid")); sess != nil {
		writeJSON(w, http.StatusOK, map[string]any{"rows": s.broker.historyRows(sess.id, 50)})
	}
}

func (s *server) doStartCall(sess *Session, w http.ResponseWriter, r *http.Request) {
	if sess.client.Store.ID == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not paired"})
		return
	}
	var body struct {
		Phone      string `json:"phone"`
		DurationMs int    `json:"duration_ms"`
		Record     bool   `json:"record"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Phone) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone required"})
		return
	}
	owner := clientID(r)
	if other := s.broker.ownerActiveCall(owner); other != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "operator already on a call"})
		return
	}
	if max := s.sessions.maxCalls; max > 0 && sess.reg.count() >= max {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "max concurrent calls"})
		return
	}
	peer := types.NewJID(normalizePhone(body.Phone), types.DefaultUserServer)

	callID, err := sess.startOutgoing(r.Context(), peer, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.broker.upsertCall(CallRecord{
		SessionID: sess.id, CallID: callID, Owner: &owner, Direction: "outbound", Peer: peer.String(),
		StartedAt: time.Now().UnixMilli(), Status: StatusRinging,
	})
	writeJSON(w, http.StatusOK, map[string]any{"call": map[string]string{"callId": callID}})
}

func (s *server) doWebRTC(sess *Session, w http.ResponseWriter, r *http.Request) {
	callID := r.PathValue("id")
	ac, ok := sess.reg.get(callID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such call"})
		return
	}
	var body struct {
		SDPOffer string `json:"sdp_offer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SDPOffer == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sdp_offer required"})
		return
	}
	bridge, answer, err := NewBridge(body.SDPOffer, s.log)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	bridge.OnBrowserPCM = func(pcm []float32) {
		ac.cm.FeedCapturedPCM(pcm)
	}
	bridge.OnTerminalICE = func() {
		go sess.terminateCall(callID, core.EndCallReasonUserEnded)
	}
	sess.setBridge(callID, bridge)
	writeJSON(w, http.StatusOK, map[string]string{"sdp_answer": answer})
}

func (s *server) doAccept(sess *Session, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ac, ok := sess.reg.get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such call"})
		return
	}
	owner := clientID(r)
	if other := s.broker.ownerActiveCall(owner); other != "" && other != id {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "operator already on a call"})
		return
	}
	if !s.broker.setOwner(id, owner) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "claimed by another client"})
		return
	}
	s.broker.emitIncomingClaimed(sess.id, id, owner)
	if err := ac.cm.AcceptCall(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"call": map[string]string{"callId": id}})
}

func (s *server) doReject(sess *Session, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if ac, ok := sess.reg.get(id); ok {
		_ = ac.cm.RejectCall(r.Context(), id, core.EndCallReasonDeclined)
	}
	sess.removeCall(id)
	s.broker.endCall(id, string(core.EndCallReasonDeclined))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) doEndCall(sess *Session, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if ac, ok := sess.reg.get(id); ok {
		_ = ac.cm.EndCall(r.Context(), core.EndCallReasonUserEnded)
	}
	sess.removeCall(id)
	s.broker.endCall(id, string(core.EndCallReasonUserEnded))
	w.WriteHeader(http.StatusNoContent)
}

func normalizePhone(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "+")
	var b strings.Builder
	for _, c := range p {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (s *server) handleChatList(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionByID(w, r.PathValue("sid")); sess != nil {
		chats, err := s.sessions.msgs.chats(r.Context(), sess.id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if chats == nil {
			chats = []ChatSummary{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
	}
}

func (s *server) handleMessageList(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	chat := r.URL.Query().Get("chat")
	if chat == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat query param required"})
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		fmt.Sscanf(v, "%d", &before)
	}
	msgs, err := s.sessions.msgs.messages(r.Context(), sess.id, chat, limit, before)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if msgs == nil {
		msgs = []StoredMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	if sess.client.Store.ID == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not paired"})
		return
	}
	var body struct {
		Number  string `json:"number"`
		ChatJid string `json:"chatJid"`
		Text    string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text required"})
		return
	}
	target := body.ChatJid
	if target == "" {
		target = body.Number
	}
	if target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "number or chatJid required"})
		return
	}
	peer := chatJIDFromInput(target)
	resp, err := sess.sendTextMessage(r.Context(), peer, body.Text)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": string(resp.ID), "timestamp": resp.Timestamp.UnixMilli()})
}

func (s *server) handleSendMedia(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	if sess.client.Store.ID == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not paired"})
		return
	}
	var body struct {
		Number      string `json:"number"`
		ChatJid     string `json:"chatJid"`
		Mediatype   string `json:"mediatype"`
		Media       string `json:"media"`
		Caption     string `json:"caption"`
		FileName    string `json:"fileName"`
		PTT         bool   `json:"ptt"`
		GIFPlayback bool   `json:"gifPlayback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Media == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "media required (base64)"})
		return
	}
	target := body.ChatJid
	if target == "" {
		target = body.Number
	}
	if target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "number or chatJid required"})
		return
	}
	if body.Mediatype == "" {
		body.Mediatype = "image"
	}
	peer := chatJIDFromInput(target)
	resp, err := sess.sendMediaMessage(r.Context(), peer, body.Mediatype, body.Media, body.Caption, body.FileName, body.PTT, body.GIFPlayback)
	if err != nil {
		if ee, ok := err.(*apiError); ok {
			writeJSON(w, ee.status, map[string]string{"error": ee.message})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": string(resp.ID), "timestamp": resp.Timestamp.UnixMilli()})
}

func (s *server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		ChatJid string `json:"chatJid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChatJid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chatJid required"})
		return
	}
	chatJID := chatJIDFromInput(body.ChatJid)
	msgs, err := s.sessions.msgs.messages(r.Context(), sess.id, body.ChatJid, 100, 0)
	if err == nil {
		var ids []string
		senderCounts := map[string][]types.MessageID{}
		for _, m := range msgs {
			if m.FromMe {
				continue
			}
			ids = append(ids, m.ID)
			senderCounts[m.SenderJID] = append(senderCounts[m.SenderJID], types.MessageID(m.ID))
		}
		now := time.Now()
		for senderStr, mids := range senderCounts {
			sender, _ := types.ParseJID(senderStr)
			_ = sess.client.MarkRead(r.Context(), mids, now, chatJID, sender)
		}
		_ = s.sessions.msgs.resetUnread(r.Context(), sess.id, body.ChatJid)
		if len(ids) > 0 {
			_ = s.sessions.msgs.updateMessageStatus(r.Context(), sess.id, ids, "READ")
			s.broker.emitRaw(map[string]any{
				"type": "message-status", "sessionId": sess.id, "ids": ids, "status": "READ",
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleSetPresence(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		Jid    string `json:"jid"`
		State  string `json:"state"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Jid != "" {
		state := types.ChatPresenceComposing
		media := types.ChatPresenceMediaText
		switch body.State {
		case "recording":
			state = types.ChatPresenceComposing
			media = types.ChatPresenceMediaAudio
		case "paused":
			state = types.ChatPresencePaused
		}
		_ = sess.client.SendChatPresence(r.Context(), chatJIDFromInput(body.Jid), state, media)
	} else {
		state := types.PresenceAvailable
		if body.State == "unavailable" {
			state = types.PresenceUnavailable
		}
		_ = sess.client.SendPresence(r.Context(), state)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleCheckNumber(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		Numbers []string `json:"numbers"`
		Number  string   `json:"number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "numbers required"})
		return
	}
	numbers := body.Numbers
	if len(numbers) == 0 && body.Number != "" {
		numbers = []string{body.Number}
	}
	if len(numbers) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "numbers required"})
		return
	}
	phones := make([]string, 0, len(numbers))
	for _, n := range numbers {
		phones = append(phones, normalizePhone(n))
	}
	res, err := sess.client.IsOnWhatsApp(r.Context(), phones)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(res))
	for _, item := range res {
		out = append(out, map[string]any{"exists": item.IsIn, "jid": item.JID.String(), "number": item.Query})
	}
	writeJSON(w, http.StatusOK, map[string]any{"numbers": out})
}

func (s *server) handleProfilePicture(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		Jid string `json:"jid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Jid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jid required"})
		return
	}
	info, err := sess.client.GetProfilePictureInfo(r.Context(), chatJIDFromInput(body.Jid), nil)
	if err != nil || info == nil {
		writeJSON(w, http.StatusOK, map[string]string{"url": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": info.URL})
}

func (s *server) handleMediaFile(w http.ResponseWriter, r *http.Request) {
	path, ok := s.media.path(r.PathValue("sid"), r.PathValue("file"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}
