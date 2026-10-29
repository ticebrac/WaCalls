package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
	"rsc.io/qr"
)

func (s *server) evoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /instance/create", s.evoRequireKey(s.evoInstanceCreate))
	mux.HandleFunc("GET /instance/fetchInstances", s.evoRequireKey(s.evoInstanceFetch))
	mux.HandleFunc("GET /instance/connect/{instance}", s.evoRequireKey(s.evoInstanceConnect))
	mux.HandleFunc("GET /instance/connectionState/{instance}", s.evoRequireKey(s.evoInstanceState))
	mux.HandleFunc("POST /instance/logout/{instance}", s.evoRequireKey(s.evoInstanceLogout))
	mux.HandleFunc("POST /instance/reconnect/{instance}", s.evoRequireKey(s.evoInstanceReconnect))
	mux.HandleFunc("DELETE /instance/{instance}", s.evoRequireKey(s.evoInstanceDelete))

	mux.HandleFunc("POST /message/sendText/{instance}", s.evoRequireKey(s.evoSendText))
	mux.HandleFunc("POST /message/sendMedia/{instance}", s.evoRequireKey(s.evoSendMedia))
	mux.HandleFunc("POST /message/react/{instance}", s.evoRequireKey(s.evoReact))
	mux.HandleFunc("POST /message/delete/{instance}", s.evoRequireKey(s.evoDeleteMessage))
	mux.HandleFunc("POST /message/edit/{instance}", s.evoRequireKey(s.evoEditMessage))
	mux.HandleFunc("POST /message/sendStatus/{instance}", s.evoRequireKey(s.evoSendStatus))

	mux.HandleFunc("POST /chat/WhatsAppNumbers/{instance}", s.evoRequireKey(s.evoWhatsAppNumbers))
	mux.HandleFunc("POST /chat/checkIsRegisteredUser/{instance}", s.evoRequireKey(s.evoCheckRegistered))
	mux.HandleFunc("POST /chat/fetchProfilePictureUrl/{instance}", s.evoRequireKey(s.evoProfilePicture))
	mux.HandleFunc("POST /chat/sendPresence/{instance}", s.evoRequireKey(s.evoSendPresence))
	mux.HandleFunc("POST /chat/readMessages/{instance}", s.evoRequireKey(s.evoReadMessages))

	mux.HandleFunc("POST /group/create/{instance}", s.evoRequireKey(s.evoGroupCreate))
	mux.HandleFunc("GET /group/all/{instance}", s.evoRequireKey(s.evoGroupAll))
	mux.HandleFunc("POST /group/inviteCode/{instance}", s.evoRequireKey(s.evoGroupInviteCode))
	mux.HandleFunc("POST /group/revokeInviteCode/{instance}", s.evoRequireKey(s.evoGroupRevokeInvite))
	mux.HandleFunc("POST /group/join/{instance}", s.evoRequireKey(s.evoGroupJoin))
	mux.HandleFunc("POST /group/leave/{instance}", s.evoRequireKey(s.evoGroupLeave))
	mux.HandleFunc("GET /group/participants/{instance}/{group}", s.evoRequireKey(s.evoGroupParticipants))
	mux.HandleFunc("POST /group/participants/add/{instance}", s.evoRequireKey(s.evoGroupParticipantsAdd))
	mux.HandleFunc("POST /group/participants/remove/{instance}", s.evoRequireKey(s.evoGroupParticipantsRemove))
	mux.HandleFunc("POST /group/participants/promote/{instance}", s.evoRequireKey(s.evoGroupParticipantsPromote))
	mux.HandleFunc("POST /group/participants/demote/{instance}", s.evoRequireKey(s.evoGroupParticipantsDemote))
	mux.HandleFunc("POST /group/updateSubject/{instance}", s.evoRequireKey(s.evoGroupUpdateSubject))
	mux.HandleFunc("POST /group/updateDescription/{instance}", s.evoRequireKey(s.evoGroupUpdateDescription))
	mux.HandleFunc("POST /group/sendMessage/{instance}", s.evoRequireKey(s.evoGroupSendMessage))

	mux.HandleFunc("POST /webhook/set/{instance}", s.evoRequireKey(s.evoWebhookSet))
	mux.HandleFunc("GET /webhook/find/{instance}", s.evoRequireKey(s.evoWebhookFind))
}

func (s *server) evoRequireKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey != "" {
			got := r.Header.Get("apikey")
			if got == "" {
				auth := r.Header.Get("Authorization")
				got = strings.TrimPrefix(auth, "Bearer ")
			}
			if got != s.apiKey {
				evoError(w, http.StatusForbidden, "FORBIDDEN", "invalid api key")
				return
			}
		}
		next(w, r)
	}
}

func evoError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"success": false,
		"error":   map[string]string{"code": code, "message": message},
	})
}

func evoSession(w http.ResponseWriter, s *server, name string) *Session {
	sess, ok := s.sessions.ByInstance(name)
	if !ok {
		evoError(w, http.StatusNotFound, "NOT_FOUND", "Instance not found")
		return nil
	}
	return sess
}

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func newWAText(text string) *waE2E.Message {
	return &waE2E.Message{Conversation: proto.String(text)}
}

func evoMessageResponse(remoteJid string, id types.MessageID, ts time.Time) map[string]any {
	sec := ts.Unix()
	if sec == 0 {
		sec = time.Now().Unix()
	}
	return map[string]any{
		"key": map[string]any{
			"remoteJid": remoteJid,
			"fromMe":    true,
			"id":        string(id),
		},
		"messageTimestamp": fmt.Sprintf("%d", sec),
		"status":           "PENDING",
	}
}

func connectionStateOf(sess *Session) string {
	sess.mu.Lock()
	st := sess.auth.State
	sess.mu.Unlock()
	switch st {
	case "open":
		return "open"
	case "logged_out":
		return "close"
	default:
		return "connecting"
	}
}

// ---------- instance ----------

func (s *server) evoInstanceCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceName string `json:"instanceName"`
		Integration  string `json:"integration"`
		Webhook      *struct {
			Enabled         bool     `json:"enabled"`
			URL             string   `json:"url"`
			WebhookByEvents bool     `json:"webhookByEvents"`
			Events          []string `json:"events"`
		} `json:"webhook"`
	}
	if err := decodeBody(r, &body); err != nil || strings.TrimSpace(body.InstanceName) == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "instanceName is required")
		return
	}
	name := strings.TrimSpace(body.InstanceName)
	if _, exists := s.sessions.ByInstance(name); exists {
		evoError(w, http.StatusConflict, "CONFLICT", "instance already exists")
		return
	}
	id, err := s.sessions.CreateNamed(name, name)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	if body.Webhook != nil && body.Webhook.URL != "" {
		cfg := &WebhookConfig{
			Enabled: body.Webhook.Enabled, URL: body.Webhook.URL,
			WebhookByEvents: body.Webhook.WebhookByEvents, Events: body.Webhook.Events,
		}
		if !cfg.Enabled {
			cfg.Enabled = true
		}
		_ = s.sessions.msgs.setWebhook(r.Context(), id, cfg)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"instance": map[string]any{"instanceName": name, "status": "created"},
		"hash":     map[string]string{"apikey": s.apiKey, "instanceName": name},
	})
}

func (s *server) evoInstanceFetch(w http.ResponseWriter, r *http.Request) {
	infos := s.sessions.infos()
	out := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		out = append(out, map[string]any{
			"id":                      info.ID,
			"name":                    info.Name,
			"instanceName":            info.ID,
			"state":                   connectionStateString(info.State),
			"integration":             "whatsmeow",
			"number":                  info.JID,
			"owner":                   info.JID,
			"profileName":             info.Name,
			"totalChat":               0,
			"totalChatWithoutMessage": 0,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func connectionStateString(state string) string {
	switch state {
	case "open":
		return "open"
	case "logged_out":
		return "close"
	default:
		return "connecting"
	}
}

func (s *server) evoInstanceConnect(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	sess.mu.Lock()
	qrCode := sess.auth.QR
	state := sess.auth.State
	paired := sess.auth.Paired
	sess.mu.Unlock()
	if paired || state == "open" {
		writeJSON(w, http.StatusOK, map[string]any{
			"pairing": false,
			"code":    "",
			"base64":  "",
			"count":   0,
		})
		return
	}
	if qrCode == "" {
		if err := s.sessions.Pair(sess.id); err != nil {
			evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		for i := 0; i < 20; i++ {
			sess.mu.Lock()
			qrCode = sess.auth.QR
			sess.mu.Unlock()
			if qrCode != "" {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	if qrCode == "" {
		evoError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "QR not ready yet, retry shortly")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pairing": true,
		"code":    qrCode,
		"base64":  qrDataURL(qrCode),
		"count":   1,
	})
}

func qrDataURL(code string) string {
	c, err := qr.Encode(code, qr.M)
	if err != nil {
		return ""
	}
	img := c.Image()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func (s *server) evoInstanceState(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": map[string]any{
			"instanceName": sess.id,
			"state":        connectionStateOf(sess),
		},
	})
}

func (s *server) evoInstanceLogout(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	if err := s.sessions.Logout(r.Context(), sess.id); err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": map[string]any{"instanceName": sess.id, "status": "logout"},
	})
}

func (s *server) evoInstanceReconnect(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	if sess.client.Store.ID != nil {
		if err := sess.client.Connect(); err != nil {
			evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
	} else {
		if err := s.sessions.Pair(sess.id); err != nil {
			evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": map[string]any{"instanceName": sess.id, "status": "connecting"},
	})
}

func (s *server) evoInstanceDelete(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	if err := s.sessions.Delete(r.Context(), sess.id); err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": map[string]any{"instanceName": sess.id, "status": "deleted"},
	})
}

// ---------- messages ----------

func (s *server) evoSendText(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number      string `json:"number"`
		Text        string `json:"text"`
		TextMessage *struct {
			Text string `json:"text"`
		} `json:"textMessage"`
		Delay            int      `json:"delay"`
		LinkPreview      bool     `json:"linkPreview"`
		MentionsEveryOne bool     `json:"mentionsEveryOne"`
		Mentioned        []string `json:"mentioned"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	text := body.Text
	if text == "" && body.TextMessage != nil {
		text = body.TextMessage.Text
	}
	if body.Number == "" || text == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number and text are required")
		return
	}
	peer := chatJIDFromInput(body.Number)
	if body.Delay > 0 {
		time.Sleep(time.Duration(body.Delay) * time.Millisecond)
	}
	resp, err := sess.sendTextMessage(r.Context(), peer, text)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, evoMessageResponse(peer.String(), resp.ID, resp.Timestamp))
}

func (s *server) evoSendMedia(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number      string `json:"number"`
		Mediatype   string `json:"mediatype"`
		Media       string `json:"media"`
		Caption     string `json:"caption"`
		FileName    string `json:"fileName"`
		PTT         bool   `json:"ptt"`
		GIFPlayback bool   `json:"gifPlayback"`
		Delay       int    `json:"delay"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if body.Number == "" || body.Media == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number and media are required")
		return
	}
	if body.Mediatype == "" {
		body.Mediatype = "image"
	}
	if body.Delay > 0 {
		time.Sleep(time.Duration(body.Delay) * time.Millisecond)
	}
	peer := chatJIDFromInput(body.Number)
	resp, err := sess.sendMediaMessage(r.Context(), peer, body.Mediatype, body.Media, body.Caption, body.FileName, body.PTT, body.GIFPlayback)
	if err != nil {
		if ee, ok := err.(*apiError); ok {
			evoError(w, ee.status, ee.code, ee.message)
			return
		}
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, evoMessageResponse(peer.String(), resp.ID, resp.Timestamp))
}

func (s *server) evoSendStatus(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		ContentType string `json:"contentType"`
		Text        string `json:"text"`
		MediaType   string `json:"mediatype"`
		Media       string `json:"media"`
		Caption     string `json:"caption"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	statusJID := types.NewJID("status", types.BroadcastServer)
	var result statusSendResult
	var err error
	if body.Media != "" && body.MediaType != "" {
		mt := body.MediaType
		if mt == "text" {
			mt = "image"
		}
		r2, e := sess.sendMediaMessage(r.Context(), statusJID, mt, body.Media, body.Caption, "", false, false)
		err = e
		if e == nil {
			result = statusSendResult{id: r2.ID, ts: r2.Timestamp}
		}
	} else if body.Text != "" {
		r2, e := sess.sendTextMessage(r.Context(), statusJID, body.Text)
		err = e
		if e == nil {
			result = statusSendResult{id: r2.ID, ts: r2.Timestamp}
		}
	} else {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "text or media required")
		return
	}
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, evoMessageResponse(statusJID.String(), result.id, result.ts))
}

type statusSendResult struct {
	id types.MessageID
	ts time.Time
}

func (s *server) evoReact(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number   string `json:"number"`
		Reaction string `json:"reaction"`
		Key      struct {
			ID          string `json:"id"`
			RemoteJid   string `json:"remoteJid"`
			FromMe      bool   `json:"fromMe"`
			Participant string `json:"participant"`
		} `json:"key"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	chat := body.Key.RemoteJid
	if chat == "" {
		chat = body.Number
	}
	if chat == "" || body.Key.ID == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "key.id and number/remoteJid are required")
		return
	}
	chatJID := chatJIDFromInput(chat)
	sender := chatJID
	if body.Key.Participant != "" {
		sender = chatJIDFromInput(body.Key.Participant)
	}
	msg := sess.client.BuildReaction(chatJID, sender, types.MessageID(body.Key.ID), body.Reaction)
	resp, err := sess.client.SendMessage(r.Context(), chatJID, msg)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, evoMessageResponse(chatJID.String(), resp.ID, resp.Timestamp))
}

func (s *server) evoDeleteMessage(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number    string `json:"number"`
		OnlyForMe bool   `json:"onlyForMe"`
		Key       struct {
			ID          string `json:"id"`
			RemoteJid   string `json:"remoteJid"`
			FromMe      bool   `json:"fromMe"`
			Participant string `json:"participant"`
		} `json:"key"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if body.Key.ID == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "key.id is required")
		return
	}
	chat := body.Key.RemoteJid
	if chat == "" {
		chat = body.Number
	}
	chatJID := chatJIDFromInput(chat)
	if body.OnlyForMe && !body.Key.FromMe {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "onlyForMe deletion is not supported for incoming messages")
		return
	}
	resp, err := sess.client.RevokeMessage(r.Context(), chatJID, types.MessageID(body.Key.ID))
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, evoMessageResponse(chatJID.String(), resp.ID, resp.Timestamp))
}

func (s *server) evoEditMessage(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number    string `json:"number"`
		Text      string `json:"text"`
		MessageID string `json:"messageId"`
		Key       struct {
			ID        string `json:"id"`
			RemoteJid string `json:"remoteJid"`
		} `json:"key"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	msgID := body.MessageID
	if msgID == "" {
		msgID = body.Key.ID
	}
	chat := body.Key.RemoteJid
	if chat == "" {
		chat = body.Number
	}
	if chat == "" || msgID == "" || body.Text == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number, messageId and text are required")
		return
	}
	chatJID := chatJIDFromInput(chat)
	msg := sess.client.BuildEdit(chatJID, types.MessageID(msgID), newWAText(body.Text))
	resp, err := sess.client.SendMessage(r.Context(), chatJID, msg)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, evoMessageResponse(chatJID.String(), resp.ID, resp.Timestamp))
}

// ---------- chat ----------

func (s *server) evoWhatsAppNumbers(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Numbers []string `json:"numbers"`
	}
	if err := decodeBody(r, &body); err != nil || len(body.Numbers) == 0 {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "numbers is required")
		return
	}
	phones := make([]string, 0, len(body.Numbers))
	for _, n := range body.Numbers {
		phones = append(phones, normalizePhone(n))
	}
	res, err := sess.client.IsOnWhatsApp(r.Context(), phones)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(res))
	for _, item := range res {
		entry := map[string]any{"exists": item.IsIn, "jid": item.JID.String(), "number": item.Query}
		if item.VerifiedName != nil && item.VerifiedName.Details != nil {
			entry["name"] = item.VerifiedName.Details.GetVerifiedName()
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) evoCheckRegistered(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number string `json:"number"`
	}
	if err := decodeBody(r, &body); err != nil || body.Number == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number is required")
		return
	}
	res, err := sess.client.IsOnWhatsApp(r.Context(), []string{normalizePhone(body.Number)})
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	exists := len(res) > 0 && res[0].IsIn
	writeJSON(w, http.StatusOK, map[string]any{"exists": exists, "number": body.Number})
}

func (s *server) evoProfilePicture(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number string `json:"number"`
		Jid    string `json:"jid"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	target := body.Jid
	if target == "" {
		target = body.Number
	}
	if target == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number or jid is required")
		return
	}
	info, err := sess.client.GetProfilePictureInfo(r.Context(), chatJIDFromInput(target), nil)
	if err != nil || info == nil {
		writeJSON(w, http.StatusOK, map[string]string{"url": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": info.URL})
}

func (s *server) evoSendPresence(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number   string `json:"number"`
		Presence string `json:"presence"`
	}
	_ = decodeBody(r, &body)
	if body.Number != "" {
		state := types.ChatPresenceComposing
		media := types.ChatPresenceMediaText
		switch body.Presence {
		case "recording":
			state = types.ChatPresenceComposing
			media = types.ChatPresenceMediaAudio
		case "paused":
			state = types.ChatPresencePaused
		}
		_ = sess.client.SendChatPresence(r.Context(), chatJIDFromInput(body.Number), state, media)
	} else {
		state := types.PresenceAvailable
		if body.Presence == "unavailable" {
			state = types.PresenceUnavailable
		}
		_ = sess.client.SendPresence(r.Context(), state)
	}
	writeJSON(w, http.StatusOK, map[string]string{"presence": "sent"})
}

func (s *server) evoReadMessages(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		ReadMessages []struct {
			ID          string `json:"id"`
			RemoteJid   string `json:"remoteJid"`
			FromMe      bool   `json:"fromMe"`
			Participant string `json:"participant"`
		} `json:"readMessages"`
	}
	if err := decodeBody(r, &body); err != nil || len(body.ReadMessages) == 0 {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "readMessages is required")
		return
	}
	byChat := map[types.JID][]types.MessageID{}
	for _, m := range body.ReadMessages {
		chat := m.RemoteJid
		if chat == "" {
			continue
		}
		chatJID := chatJIDFromInput(chat)
		byChat[chatJID] = append(byChat[chatJID], types.MessageID(m.ID))
	}
	now := time.Now()
	for chatJID, ids := range byChat {
		_ = sess.client.MarkRead(r.Context(), ids, now, chatJID, types.JID{})
	}
	ids := make([]string, 0, len(body.ReadMessages))
	for _, m := range body.ReadMessages {
		ids = append(ids, m.ID)
	}
	_ = sess.mgr.msgs.updateMessageStatus(r.Context(), sess.id, ids, "READ")
	sess.mgr.broker.emitRaw(map[string]any{
		"type": "message-status", "sessionId": sess.id, "ids": ids, "status": "READ",
	})
	writeJSON(w, http.StatusOK, map[string]any{"read": len(ids)})
}

// ---------- groups ----------

func (s *server) evoGroupCreate(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Subject      string   `json:"subject"`
		Participants []string `json:"participants"`
	}
	if err := decodeBody(r, &body); err != nil || body.Subject == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "subject is required")
		return
	}
	jids := make([]types.JID, 0, len(body.Participants))
	for _, p := range body.Participants {
		jids = append(jids, userJID(p))
	}
	info, err := sess.client.CreateGroup(r.Context(), whatsmeow.ReqCreateGroup{Name: body.Subject, Participants: jids})
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"creator": sess.jidString(),
		"id":      info.JID.String(),
		"subject": info.Name,
	})
}

func (s *server) evoGroupAll(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	groups, err := sess.client.GetJoinedGroups(r.Context())
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupInfoMap(g.JID.String(), g.Name, g.Participants))
	}
	writeJSON(w, http.StatusOK, out)
}

func groupInfoMap(jid, subject string, participants []types.GroupParticipant) map[string]any {
	parts := make([]map[string]any, 0, len(participants))
	for _, p := range participants {
		parts = append(parts, map[string]any{
			"id":      p.JID.String(),
			"admin":   participantAdmin(p),
			"isAdmin": p.IsAdmin,
			"isSuper": p.IsSuperAdmin,
		})
	}
	return map[string]any{
		"id":           jid,
		"subject":      subject,
		"owner":        "",
		"participants": parts,
		"size":         len(participants),
	}
}

func participantAdmin(p types.GroupParticipant) string {
	switch {
	case p.IsSuperAdmin:
		return "superadmin"
	case p.IsAdmin:
		return "admin"
	default:
		return ""
	}
}

func (s *server) evoGroupInviteCode(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number string `json:"number"`
	}
	if err := decodeBody(r, &body); err != nil || body.Number == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number (group jid) is required")
		return
	}
	link, err := sess.client.GetGroupInviteLink(r.Context(), chatJIDFromInput(body.Number), false)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inviteCode": inviteCodeFromLink(link), "inviteUrl": link})
}

func inviteCodeFromLink(link string) string {
	const prefix = "https://chat.whatsapp.com/"
	if strings.HasPrefix(link, prefix) {
		return strings.TrimPrefix(link, prefix)
	}
	return link
}

func (s *server) evoGroupRevokeInvite(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number string `json:"number"`
	}
	if err := decodeBody(r, &body); err != nil || body.Number == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number (group jid) is required")
		return
	}
	link, err := sess.client.GetGroupInviteLink(r.Context(), chatJIDFromInput(body.Number), true)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inviteCode": inviteCodeFromLink(link), "inviteUrl": link})
}

func (s *server) evoGroupJoin(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		InviteCode string `json:"inviteCode"`
	}
	if err := decodeBody(r, &body); err != nil || body.InviteCode == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "inviteCode is required")
		return
	}
	code := inviteCodeFromLink(body.InviteCode)
	jid, err := sess.client.JoinGroupWithLink(r.Context(), code)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "group": jid.String()})
}

func (s *server) evoGroupLeave(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number string `json:"number"`
	}
	if err := decodeBody(r, &body); err != nil || body.Number == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number (group jid) is required")
		return
	}
	if err := sess.client.LeaveGroup(r.Context(), chatJIDFromInput(body.Number)); err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (s *server) evoGroupParticipants(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	groupJID := chatJIDFromInput(r.PathValue("group"))
	info, err := sess.client.GetGroupInfo(r.Context(), groupJID)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groupInfoMap(info.JID.String(), info.Name, info.Participants))
}

func (s *server) evoGroupParticipantsAdd(w http.ResponseWriter, r *http.Request) {
	s.evoGroupParticipantsChange(w, r, whatsmeow.ParticipantChangeAdd)
}

func (s *server) evoGroupParticipantsRemove(w http.ResponseWriter, r *http.Request) {
	s.evoGroupParticipantsChange(w, r, whatsmeow.ParticipantChangeRemove)
}

func (s *server) evoGroupParticipantsPromote(w http.ResponseWriter, r *http.Request) {
	s.evoGroupParticipantsChange(w, r, whatsmeow.ParticipantChangePromote)
}

func (s *server) evoGroupParticipantsDemote(w http.ResponseWriter, r *http.Request) {
	s.evoGroupParticipantsChange(w, r, whatsmeow.ParticipantChangeDemote)
}

func (s *server) evoGroupParticipantsChange(w http.ResponseWriter, r *http.Request, action whatsmeow.ParticipantChange) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		GroupJid     string   `json:"groupJid"`
		Number       string   `json:"number"`
		Participants []string `json:"participants"`
	}
	if err := decodeBody(r, &body); err != nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	groupStr := body.GroupJid
	if groupStr == "" {
		groupStr = body.Number
	}
	if groupStr == "" || len(body.Participants) == 0 {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "groupJid and participants are required")
		return
	}
	jids := make([]types.JID, 0, len(body.Participants))
	for _, p := range body.Participants {
		jids = append(jids, userJID(p))
	}
	participants, err := sess.client.UpdateGroupParticipants(r.Context(), chatJIDFromInput(groupStr), jids, action)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(participants))
	for _, p := range participants {
		out = append(out, map[string]any{
			"id":      p.JID.String(),
			"admin":   participantAdmin(p),
			"isAdmin": p.IsAdmin,
			"isSuper": p.IsSuperAdmin,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"participants": out})
}

func (s *server) evoGroupUpdateSubject(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number  string `json:"number"`
		Subject string `json:"subject"`
	}
	if err := decodeBody(r, &body); err != nil || body.Number == "" || body.Subject == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number (group jid) and subject are required")
		return
	}
	if err := sess.client.SetGroupName(r.Context(), chatJIDFromInput(body.Number), body.Subject); err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (s *server) evoGroupUpdateDescription(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number      string `json:"number"`
		Description string `json:"description"`
	}
	if err := decodeBody(r, &body); err != nil || body.Number == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number (group jid) is required")
		return
	}
	if err := sess.client.SetGroupDescription(r.Context(), chatJIDFromInput(body.Number), body.Description); err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (s *server) evoGroupSendMessage(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Number string `json:"number"`
		Text   string `json:"text"`
	}
	if err := decodeBody(r, &body); err != nil || body.Number == "" || body.Text == "" {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "number (group jid) and text are required")
		return
	}
	peer := chatJIDFromInput(body.Number)
	resp, err := sess.sendTextMessage(r.Context(), peer, body.Text)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, evoMessageResponse(peer.String(), resp.ID, resp.Timestamp))
}

// ---------- webhooks ----------

func (s *server) evoWebhookSet(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	var body struct {
		Webhook *WebhookConfig `json:"webhook"`
	}
	if err := decodeBody(r, &body); err != nil || body.Webhook == nil {
		evoError(w, http.StatusBadRequest, "BAD_REQUEST", "webhook object is required")
		return
	}
	cfg := body.Webhook
	if err := s.sessions.msgs.setWebhook(r.Context(), sess.id, cfg); err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": cfg})
}

func (s *server) evoWebhookFind(w http.ResponseWriter, r *http.Request) {
	sess := evoSession(w, s, r.PathValue("instance"))
	if sess == nil {
		return
	}
	cfg, err := s.sessions.msgs.getWebhook(r.Context(), sess.id)
	if err != nil {
		evoError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	if cfg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"webhooks": map[string]any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": cfg})
}
