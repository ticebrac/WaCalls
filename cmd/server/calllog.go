package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CallLog is the persisted record of a call, intended for CRM integration.
type CallLog struct {
	SessionID    string `json:"sessionId"`
	CallID       string `json:"callId"`
	Direction    string `json:"direction"`
	Peer         string `json:"peer"`
	Owner        string `json:"owner,omitempty"`
	StartedAt    int64  `json:"startedAt"`
	EndedAt      int64  `json:"endedAt"`
	DurationMs   int64  `json:"durationMs"`
	RecordingMs  int64  `json:"recordingMs"`
	EndReason    string `json:"endReason"`
	RecordingURL string `json:"recordingUrl,omitempty"`
	recording    string
}

const callLogSchema = `CREATE TABLE IF NOT EXISTS calls (
	session_id   TEXT NOT NULL,
	call_id      TEXT NOT NULL,
	direction    TEXT NOT NULL,
	peer         TEXT NOT NULL,
	owner        TEXT,
	started_at   INTEGER NOT NULL,
	ended_at     INTEGER NOT NULL,
	end_reason   TEXT NOT NULL DEFAULT '',
	recording_ms INTEGER NOT NULL DEFAULT 0,
	recording    TEXT,
	PRIMARY KEY (session_id, call_id)
)`

func (s *msgStore) insertCallLog(ctx context.Context, c CallLog) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO calls
		(session_id, call_id, direction, peer, owner, started_at, ended_at, end_reason, recording_ms, recording)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, call_id) DO UPDATE SET
			ended_at = excluded.ended_at, end_reason = excluded.end_reason,
			recording_ms = excluded.recording_ms, recording = excluded.recording, owner = excluded.owner`,
		c.SessionID, c.CallID, c.Direction, c.Peer, nullIfEmpty(c.Owner), c.StartedAt, c.EndedAt,
		c.EndReason, c.RecordingMs, nullIfEmpty(c.recording))
	return err
}

func scanCallLog(sc interface{ Scan(...any) error }) (CallLog, error) {
	var c CallLog
	var owner, rec sql.NullString
	err := sc.Scan(&c.SessionID, &c.CallID, &c.Direction, &c.Peer, &owner, &c.StartedAt, &c.EndedAt,
		&c.EndReason, &c.RecordingMs, &rec)
	if err != nil {
		return c, err
	}
	c.Owner = owner.String
	c.DurationMs = c.EndedAt - c.StartedAt
	if rec.Valid && rec.String != "" {
		c.recording = rec.String
		c.RecordingURL = recordingURL(c.SessionID, rec.String)
	}
	return c, nil
}

const callLogColumns = `session_id, call_id, direction, peer, owner, started_at, ended_at, end_reason, recording_ms, recording`

func (s *msgStore) listCallLogs(ctx context.Context, sessionID, peer string, limit int) ([]CallLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if peer != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT `+callLogColumns+` FROM calls
			WHERE session_id = ? AND peer = ? ORDER BY started_at DESC LIMIT ?`, sessionID, peer, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+callLogColumns+` FROM calls
			WHERE session_id = ? ORDER BY started_at DESC LIMIT ?`, sessionID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CallLog{}
	for rows.Next() {
		c, err := scanCallLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *msgStore) getCallLog(ctx context.Context, sessionID, callID string) (CallLog, error) {
	return scanCallLog(s.db.QueryRowContext(ctx, `SELECT `+callLogColumns+` FROM calls
		WHERE session_id = ? AND call_id = ?`, sessionID, callID))
}

func recordingURL(sessionID, name string) string {
	return "/api/recordings/" + sanitizeSegment(sessionID) + "/" + name
}

// recorderFor returns the active recorder for a call, or nil when recording is off.
func (s *Session) recorderFor(callID string) *callRecorder {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	return s.recs[callID]
}

func (s *Session) startRecording(callID string, meta CallRecord) {
	dir := filepath.Join(s.mgr.recDir, sanitizeSegment(s.id))
	r, err := newCallRecorder(dir, callID, meta)
	if err != nil {
		s.log.Warn("call recording disabled", "call_id", callID, "err", err)
		return
	}
	s.recMu.Lock()
	s.recs[callID] = r
	s.recMu.Unlock()
}

// finalizeRecording closes the call's recording, persists the call log row and,
// when audio was captured, posts a note to the lead's chat that only the operator sees.
func (s *Session) finalizeRecording(callID string, ended CallRecord, reason string) {
	s.recMu.Lock()
	r := s.recs[callID]
	delete(s.recs, callID)
	s.recMu.Unlock()

	if r != nil {
		if ended.Peer == "" {
			ended.Peer = r.meta.Peer
		}
		if ended.Direction == "" {
			ended.Direction = r.meta.Direction
		}
		if ended.StartedAt == 0 {
			ended.StartedAt = r.meta.StartedAt
		}
	}
	if ended.EndedAt == nil {
		now := time.Now().UnixMilli()
		ended.EndedAt = &now
	}
	if reason == "" {
		reason = ended.EndReason
	}

	name, audioMs, err := r.finish()
	if err != nil {
		s.log.Warn("finalize recording failed", "call_id", callID, "err", err)
	}

	entry := CallLog{
		SessionID:   s.id,
		CallID:      callID,
		Direction:   ended.Direction,
		Peer:        ended.Peer,
		StartedAt:   ended.StartedAt,
		EndedAt:     *ended.EndedAt,
		EndReason:   reason,
		RecordingMs: audioMs,
		recording:   name,
	}
	if ended.Owner != nil {
		entry.Owner = *ended.Owner
	}
	if err := s.mgr.msgs.insertCallLog(context.Background(), entry); err != nil {
		s.log.Warn("persist call log failed", "call_id", callID, "err", err)
	}
	if name != "" {
		entry.RecordingURL = recordingURL(s.id, name)
		s.postCallNote(entry)
	}
}

// postCallNote stores an internal chat entry linking to the recording. It is
// never sent to WhatsApp; it only exists in the local message store.
func (s *Session) postCallNote(c CallLog) {
	ctx := context.Background()
	// The bubble reports how long the call lasted; the recorder can run a few
	// seconds shorter than the call itself.
	dur := time.Duration(c.EndedAt-c.StartedAt) * time.Millisecond
	if dur < 0 {
		dur = 0
	}
	stored := &StoredMessage{
		ID:        "call-" + c.CallID,
		SessionID: s.id,
		ChatJID:   c.Peer,
		FromMe:    true,
		Type:      "call_recording",
		Text:      fmt.Sprintf("%02d:%02d", int(dur.Minutes()), int(dur.Seconds())%60),
		MediaURL:  c.RecordingURL,
		MimeType:  "audio/wav",
		Status:    "INTERNAL",
		Timestamp: time.Now().UnixMilli(),
	}
	if err := s.mgr.msgs.insertMessage(ctx, stored); err != nil {
		s.log.Warn("post call note failed", "call_id", c.CallID, "err", err)
		return
	}
	s.mgr.broker.emitMessage(stored)
}

// handleCallEnded is wired to Broker.OnCallEnded.
func (m *SessionManager) handleCallEnded(ended CallRecord, reason string) {
	sess, ok := m.Get(ended.SessionID)
	if !ok {
		return
	}
	go sess.finalizeRecording(ended.CallID, ended, reason)
}

func (s *server) handleSessionQR(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleCallList(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	peer := normalizePhone(r.URL.Query().Get("peer"))
	if peer != "" {
		peer = withDefaultCountry(peer) + "@s.whatsapp.net"
	}
	calls, err := s.sessions.msgs.listCallLogs(r.Context(), sess.id, peer, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": calls})
}

func (s *server) handleCallGet(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionByID(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	c, err := s.sessions.msgs.getCallLog(r.Context(), sess.id, r.PathValue("id"))
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such call"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) handleRecording(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || !strings.HasSuffix(name, ".wav") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.sessions.recDir, sanitizeSegment(r.PathValue("sid")), name)
	http.ServeFile(w, r, full)
}
