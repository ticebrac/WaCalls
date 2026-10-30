package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wacalls/internal/voip/core"
)

// ---------------------------------------------------------------------------
// Authentication
//
// The CRM API is protected by two headers on every request:
//   - apikey: the shared key started with -api-key (same key the Evolution
//     routes use);
//   - X-User-Id: the collaborator making the request. It is the identity used
//     for lead ownership, call ownership and call concurrency limits.
// ---------------------------------------------------------------------------

type crmUserKey struct{}

func crmUserID(r *http.Request) string {
	v, _ := r.Context().Value(crmUserKey{}).(string)
	return v
}

func (s *server) crmAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "CRM API disabled: start the server with -api-key",
			})
			return
		}
		got := r.Header.Get("apikey")
		if got == "" {
			got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if got != s.apiKey {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid api key"})
			return
		}
		uid := strings.TrimSpace(r.Header.Get("X-User-Id"))
		if uid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-User-Id header required"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), crmUserKey{}, uid)))
	}
}

func (s *server) crmRoutes(mux *http.ServeMux) {
	// Monitor
	mux.HandleFunc("GET /api/crm/status", s.crmAuth(s.crmStatus))
	mux.HandleFunc("GET /api/crm/events", s.crmAuth(s.crmEvents))

	// Sessions / instances (CRM settings)
	mux.HandleFunc("GET /api/crm/sessions", s.crmAuth(s.crmSessionList))
	mux.HandleFunc("POST /api/crm/sessions", s.crmAuth(s.crmSessionCreate))
	mux.HandleFunc("PATCH /api/crm/sessions/{sid}", s.crmAuth(s.crmSessionRename))
	mux.HandleFunc("DELETE /api/crm/sessions/{sid}", s.crmAuth(s.crmSessionDelete))
	mux.HandleFunc("GET /api/crm/sessions/{sid}/qr", s.crmAuth(s.crmSessionQR))
	mux.HandleFunc("POST /api/crm/sessions/{sid}/disconnect", s.crmAuth(s.crmSessionDisconnect))

	// Collaborator origin instance
	mux.HandleFunc("GET /api/crm/origin", s.crmAuth(s.crmOriginGet))
	mux.HandleFunc("PUT /api/crm/origin", s.crmAuth(s.crmOriginSet))
	mux.HandleFunc("DELETE /api/crm/origin", s.crmAuth(s.crmOriginClear))

	// Leads
	mux.HandleFunc("GET /api/crm/leads", s.crmAuth(s.crmLeadList))
	mux.HandleFunc("POST /api/crm/leads", s.crmAuth(s.crmLeadClaim))
	mux.HandleFunc("DELETE /api/crm/leads/{phone}", s.crmAuth(s.crmLeadDelete))
	mux.HandleFunc("GET /api/crm/leads/{phone}/messages", s.crmAuth(s.crmLeadMessages))
	mux.HandleFunc("POST /api/crm/leads/{phone}/messages", s.crmAuth(s.crmLeadSend))

	// Calls
	mux.HandleFunc("POST /api/crm/calls", s.crmAuth(s.crmCallStart))
	mux.HandleFunc("GET /api/crm/calls", s.crmAuth(s.crmCallList))
	mux.HandleFunc("POST /api/crm/calls/{id}/end", s.crmAuth(s.crmCallEnd))
}

// crmClaim resolves the lead behind a phone number, claiming it for the caller
// on first touch (the CRM claims when a lead modal opens). It writes an error
// response and returns nil when the request must not continue.
func (s *server) crmClaim(w http.ResponseWriter, r *http.Request, phone, name string) *crmLead {
	uid := crmUserID(r)
	digits := withDefaultCountry(normalizePhone(phone))
	if digits == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone required"})
		return nil
	}
	// Ownership is settled first: a lead someone else already clicked always
	// answers 403, never a configuration error that hides the real reason.
	existing, err := s.crm.getLead(r.Context(), digits)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil
	}
	if existing != nil && existing.Owner != uid {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "lead is assigned to another user"})
		return nil
	}
	origin, err := s.crm.getOrigin(r.Context(), uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil
	}
	if origin == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "no origin instance assigned; set one in CRM settings first",
		})
		return nil
	}
	lead, err := s.crm.claimLead(r.Context(), digits, name, uid, origin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil
	}
	if lead == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lead could not be claimed"})
		return nil
	}
	if lead.Owner != uid {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "lead is assigned to another user"})
		return nil
	}
	return lead
}

// ---------------------------------------------------------------------------
// Monitor
// ---------------------------------------------------------------------------

func (s *server) crmStatus(w http.ResponseWriter, r *http.Request) {
	origin, err := s.crm.getOrigin(r.Context(), crmUserID(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"origin":      origin,
		"sessions":    s.sessions.infos(),
		"activeCalls": s.broker.activeCalls(),
	})
}

// ---------------------------------------------------------------------------
// Sessions / instances (CRM settings)
// ---------------------------------------------------------------------------

func (s *server) crmSessionList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.sessions.infos()})
}

func (s *server) crmSessionCreate(w http.ResponseWriter, r *http.Request) {
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

func (s *server) crmSessionRename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if err := s.sessions.Rename(r.Context(), r.PathValue("sid"), name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) crmSessionDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Delete(r.Context(), r.PathValue("sid")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) crmSessionDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Logout(r.Context(), r.PathValue("sid")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) crmSessionQR(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	sess.mu.Lock()
	a := sess.auth
	sess.mu.Unlock()
	out := map[string]any{"state": a.State, "paired": a.Paired || sess.client.Store.ID != nil}
	if a.QR != "" && !a.Paired {
		out["qr"] = a.QR
		out["qrBase64"] = qrDataURL(a.QR)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Collaborator origin instance
// ---------------------------------------------------------------------------

func (s *server) crmOriginGet(w http.ResponseWriter, r *http.Request) {
	origin, err := s.crm.getOrigin(r.Context(), crmUserID(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sessionId": origin})
}

func (s *server) crmOriginSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sid := strings.TrimSpace(body.SessionID)
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionId required"})
		return
	}
	if _, ok := s.sessions.Get(sid); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such session"})
		return
	}
	if err := s.crm.setOrigin(r.Context(), crmUserID(r), sid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) crmOriginClear(w http.ResponseWriter, r *http.Request) {
	if err := s.crm.clearOrigin(r.Context(), crmUserID(r)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Leads
// ---------------------------------------------------------------------------

func (s *server) crmLeadList(w http.ResponseWriter, r *http.Request) {
	leads, err := s.crm.listLeads(r.Context(), crmUserID(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leads": leads})
}

func (s *server) crmLeadClaim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Phone) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone required"})
		return
	}
	if lead := s.crmClaim(w, r, body.Phone, strings.TrimSpace(body.Name)); lead != nil {
		writeJSON(w, http.StatusOK, map[string]any{"lead": lead})
	}
}

// crmLeadDelete releases the claim on a number. Only the current owner (or any
// key holder, since claims are administrative) can clear it; afterwards the
// number can be claimed again by whoever clicks next.
func (s *server) crmLeadDelete(w http.ResponseWriter, r *http.Request) {
	phone := withDefaultCountry(normalizePhone(r.PathValue("phone")))
	if phone == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone required"})
		return
	}
	ok, err := s.crm.deleteLead(r.Context(), phone)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such lead"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) crmLeadMessages(w http.ResponseWriter, r *http.Request) {
	lead := s.crmClaim(w, r, r.PathValue("phone"), "")
	if lead == nil {
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	var before int64
	if n, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil {
		before = n
	}
	msgs, err := s.sessions.msgs.messages(r.Context(), lead.SessionID, chatJIDFromInput(lead.Phone).String(), limit, before)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The store answers newest-first; a conversation panel draws top-down.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"lead": lead, "messages": msgs})
}

func (s *server) crmLeadSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text required"})
		return
	}
	lead := s.crmClaim(w, r, r.PathValue("phone"), "")
	if lead == nil {
		return
	}
	sess, ok := s.sessions.Get(lead.SessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance for this lead no longer exists"})
		return
	}
	if sess.client.Store.ID == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not paired"})
		return
	}
	peer := sess.canonicalJIDFromInput(r.Context(), lead.Phone)
	resp, err := sess.sendTextMessage(r.Context(), peer, body.Text)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": string(resp.ID), "timestamp": resp.Timestamp.UnixMilli(),
	})
}

// ---------------------------------------------------------------------------
// Calls
// ---------------------------------------------------------------------------

func (s *server) crmCallStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone      string `json:"phone"`
		Name       string `json:"name"`
		Record     *bool  `json:"record"`
		DurationMs int    `json:"duration_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Phone) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone required"})
		return
	}
	uid := crmUserID(r)
	lead := s.crmClaim(w, r, body.Phone, strings.TrimSpace(body.Name))
	if lead == nil {
		return
	}
	sess, ok := s.sessions.Get(lead.SessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance for this lead no longer exists"})
		return
	}
	if sess.client.Store.ID == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not paired"})
		return
	}
	if other := s.broker.ownerActiveCall(uid); other != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "operator already on a call"})
		return
	}
	if s.sessions.maxCalls > 0 && sess.reg.count() >= s.sessions.maxCalls {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "max concurrent calls"})
		return
	}
	peer := sess.peerFromInput(r.Context(), lead.Phone)
	record := body.Record == nil || *body.Record
	callID, err := sess.startOutgoing(r.Context(), peer, false, record)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.broker.upsertCall(CallRecord{
		SessionID: lead.SessionID, CallID: callID, Owner: &uid,
		Direction: "outbound", Peer: peer.String(),
		StartedAt: time.Now().UnixMilli(), Status: StatusRinging,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"call": map[string]string{"callId": callID, "sessionId": lead.SessionID},
	})
}

func (s *server) crmCallList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	calls, err := s.crm.listCalls(r.Context(), crmUserID(r), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": calls})
}

func (s *server) crmCallEnd(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := crmUserID(r)
	call, ok := s.broker.getCall(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such call"})
		return
	}
	if call.Owner == nil || *call.Owner != uid {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "call belongs to another user"})
		return
	}
	sess, ok := s.sessions.Get(call.SessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such session"})
		return
	}
	if ac, ok := sess.reg.get(id); ok {
		_ = ac.cm.EndCall(r.Context(), core.EndCallReasonUserEnded)
	}
	sess.removeCall(id)
	s.broker.endCall(id, string(core.EndCallReasonUserEnded))
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Events (SSE)
//
// The shared stream carries every event in the server; the CRM stream forwards
// only what its collaborator may see — session/QR state for settings, messages
// of their own leads and call events they own. Everything else (other people's
// messages, presence, incoming rings) is dropped.
// ---------------------------------------------------------------------------

type crmEvent struct {
	Type    string         `json:"type"`
	Owner   *string        `json:"owner"`
	Message *StoredMessage `json:"message"`
	Calls   []CallRecord   `json:"calls"`
}

func (s *server) crmEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	uid := crmUserID(r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sub := s.broker.subscribe(uid)
	defer s.broker.unsubscribe(sub)

	if s.broker.SnapshotFn != nil {
		for _, ev := range s.broker.SnapshotFn() {
			writeSSE(w, flusher, ev)
		}
	}
	if data, ok := s.crmCallListEvent(uid); ok {
		w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		flusher.Flush()
	}

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-sub.ch:
			out, keep := s.crmEventFor(r.Context(), uid, data)
			if !keep {
				continue
			}
			if _, err := w.Write(append(append([]byte("data: "), out...), '\n', '\n')); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}
	}
}

func (s *server) crmCallListEvent(uid string) ([]byte, bool) {
	owned := make([]CallRecord, 0)
	for _, c := range s.broker.activeCalls() {
		if c.Owner != nil && *c.Owner == uid {
			owned = append(owned, c)
		}
	}
	data, err := json.Marshal(map[string]any{"type": "call-list", "calls": owned})
	if err != nil {
		return nil, false
	}
	return data, true
}

// crmEventFor decides whether a broadcast event belongs to this collaborator,
// rewriting call lists down to their calls.
func (s *server) crmEventFor(ctx context.Context, uid string, data []byte) ([]byte, bool) {
	var ev crmEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, false
	}
	switch ev.Type {
	case "session-list", "auth-state", "session-qr":
		return data, true
	case "message":
		if ev.Message == nil {
			return nil, false
		}
		return data, s.crm.ownsChat(ctx, uid, ev.Message.ChatJID)
	case "call-status", "call-ended":
		return data, ev.Owner != nil && *ev.Owner == uid
	case "call-list":
		owned := make([]CallRecord, 0, len(ev.Calls))
		for _, c := range ev.Calls {
			if c.Owner != nil && *c.Owner == uid {
				owned = append(owned, c)
			}
		}
		out, err := json.Marshal(map[string]any{"type": "call-list", "calls": owned})
		if err != nil {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}
