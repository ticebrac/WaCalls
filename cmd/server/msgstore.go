package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type StoredMessage struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	ChatJID   string `json:"chatJid"`
	SenderJID string `json:"senderJid"`
	FromMe    bool   `json:"fromMe"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	FileName  string `json:"fileName,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	MediaURL  string `json:"mediaUrl,omitempty"`
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	PushName  string `json:"pushName,omitempty"`
}

type ChatSummary struct {
	SessionID  string `json:"sessionId"`
	JID        string `json:"jid"`
	Name       string `json:"name"`
	IsGroup    bool   `json:"isGroup"`
	Unread     int    `json:"unread"`
	LastText   string `json:"lastText"`
	LastType   string `json:"lastType"`
	LastFromMe bool   `json:"lastFromMe"`
	LastAt     int64  `json:"lastAt"`
}

type WebhookConfig struct {
	Enabled         bool     `json:"enabled"`
	URL             string   `json:"url"`
	WebhookByEvents bool     `json:"webhookByEvents"`
	Events          []string `json:"events"`
}

type msgStore struct{ db *qDB }

func newMsgStore(ctx context.Context, db *qDB) (*msgStore, error) {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS messages (
			id         TEXT NOT NULL,
			session_id TEXT NOT NULL,
			chat_jid   TEXT NOT NULL,
			sender_jid TEXT NOT NULL,
			from_me    INTEGER NOT NULL,
			type       TEXT NOT NULL,
			text       TEXT NOT NULL DEFAULT '',
			file_name  TEXT,
			mime_type  TEXT,
			media_path TEXT,
			status     TEXT NOT NULL DEFAULT 'RECEIVED',
			ts         INTEGER NOT NULL,
			push_name  TEXT,
			PRIMARY KEY (session_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat ON messages(session_id, chat_jid, ts)`,
		`CREATE TABLE IF NOT EXISTS chats (
			session_id TEXT NOT NULL,
			jid        TEXT NOT NULL,
			name       TEXT NOT NULL DEFAULT '',
			is_group   INTEGER NOT NULL DEFAULT 0,
			unread     INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (session_id, jid)
		)`,
		`CREATE TABLE IF NOT EXISTS webhooks (
			session_id       TEXT PRIMARY KEY,
			enabled          INTEGER NOT NULL DEFAULT 0,
			url              TEXT NOT NULL DEFAULT '',
			webhook_by_events INTEGER NOT NULL DEFAULT 0,
			events           TEXT NOT NULL DEFAULT '[]'
		)`,
		callLogSchema,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return nil, err
		}
	}
	return &msgStore{db: db}, nil
}

func (s *msgStore) insertMessage(ctx context.Context, m *StoredMessage) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO messages
		(id, session_id, chat_jid, sender_jid, from_me, type, text, file_name, mime_type, media_path, status, ts, push_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, id) DO UPDATE SET
			chat_jid = excluded.chat_jid, sender_jid = excluded.sender_jid, from_me = excluded.from_me,
			type = excluded.type, text = excluded.text, file_name = excluded.file_name,
			mime_type = excluded.mime_type, media_path = excluded.media_path, status = excluded.status,
			ts = excluded.ts, push_name = excluded.push_name`,
		m.ID, m.SessionID, m.ChatJID, m.SenderJID, boolToInt(m.FromMe), m.Type, m.Text,
		nullIfEmpty(m.FileName), nullIfEmpty(m.MimeType), nullIfEmpty(m.MediaURL), m.Status, m.Timestamp, nullIfEmpty(m.PushName),
	)
	if err != nil {
		return err
	}
	return s.touchChat(ctx, m)
}

func (s *msgStore) touchChat(ctx context.Context, m *StoredMessage) error {
	if m.ChatJID == "" {
		return nil
	}
	name := m.PushName
	if m.FromMe || name == "" {
		name = jidDisplay(m.ChatJID)
	}
	isGroup := 0
	unread := 0
	if isGroupJID(m.ChatJID) {
		isGroup = 1
	}
	if !m.FromMe {
		unread = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO chats (session_id, jid, name, is_group, unread, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, jid) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
			unread = CASE WHEN excluded.unread = 1 THEN chats.unread + 1 ELSE chats.unread END,
			updated_at = excluded.updated_at`,
		m.SessionID, m.ChatJID, name, isGroup, unread, m.Timestamp,
	)
	return err
}

func (s *msgStore) setChatName(ctx context.Context, sessionID, jid, name string) error {
	if name == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO chats (session_id, jid, name, is_group, unread, updated_at)
		VALUES (?, ?, ?, ?, 0, ?)
		ON CONFLICT(session_id, jid) DO UPDATE SET name = excluded.name`,
		sessionID, jid, name, boolToInt(isGroupJID(jid)), time.Now().UnixMilli())
	return err
}

func (s *msgStore) resetUnread(ctx context.Context, sessionID, chatJID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET unread = 0 WHERE session_id = ? AND jid = ?`, sessionID, chatJID)
	return err
}

func (s *msgStore) updateMessageStatus(ctx context.Context, sessionID string, ids []string, status string) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `UPDATE messages SET status = ? WHERE session_id = ? AND id = ? AND from_me = 1`,
			status, sessionID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *msgStore) chats(ctx context.Context, sessionID string) ([]ChatSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.session_id, c.jid, c.name, c.is_group, c.unread,
		COALESCE(m.text, ''), COALESCE(m.type, ''), COALESCE(m.from_me, 0), COALESCE(m.ts, 0)
		FROM chats c
		LEFT JOIN messages m ON m.session_id = c.session_id AND m.id = (
			SELECT id FROM messages WHERE session_id = c.session_id AND chat_jid = c.jid ORDER BY ts DESC, id DESC LIMIT 1
		)
		WHERE c.session_id = ?
		ORDER BY COALESCE(m.ts, c.updated_at) DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var c ChatSummary
		var isGroup, lastFromMe int
		if err := rows.Scan(&c.SessionID, &c.JID, &c.Name, &isGroup, &c.Unread, &c.LastText, &c.LastType, &lastFromMe, &c.LastAt); err != nil {
			return nil, err
		}
		c.IsGroup = isGroup == 1
		c.LastFromMe = lastFromMe == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *msgStore) messages(ctx context.Context, sessionID, chatJID string, limit int, before int64) ([]StoredMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if before > 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT id, session_id, chat_jid, sender_jid, from_me, type, text,
			COALESCE(file_name, ''), COALESCE(mime_type, ''), COALESCE(media_path, ''), status, ts, COALESCE(push_name, '')
			FROM messages WHERE session_id = ? AND chat_jid = ? AND ts < ? ORDER BY ts DESC, id DESC LIMIT ?`,
			sessionID, chatJID, before, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id, session_id, chat_jid, sender_jid, from_me, type, text,
			COALESCE(file_name, ''), COALESCE(mime_type, ''), COALESCE(media_path, ''), status, ts, COALESCE(push_name, '')
			FROM messages WHERE session_id = ? AND chat_jid = ? ORDER BY ts DESC, id DESC LIMIT ?`,
			sessionID, chatJID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var fromMe int
		if err := rows.Scan(&m.ID, &m.SessionID, &m.ChatJID, &m.SenderJID, &fromMe, &m.Type, &m.Text,
			&m.FileName, &m.MimeType, &m.MediaURL, &m.Status, &m.Timestamp, &m.PushName); err != nil {
			return nil, err
		}
		m.FromMe = fromMe == 1
		// Older rows store only the cached file name; expose it as a served URL.
		if m.MediaURL != "" && !strings.HasPrefix(m.MediaURL, "/") {
			m.MediaURL = "/api/media/" + sanitizeSegment(m.SessionID) + "/" + m.MediaURL
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *msgStore) setMessageMedia(ctx context.Context, sessionID, msgID, mediaPath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET media_path = ? WHERE session_id = ? AND id = ?`,
		mediaPath, sessionID, msgID)
	return err
}

// listLIDChats returns the chat rows still keyed by a hidden LID address.
func (s *msgStore) listLIDChats(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT jid FROM chats WHERE session_id = ? AND jid LIKE '%@lid'`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		out = append(out, jid)
	}
	return out, rows.Err()
}

// hasChat reports whether the session already has a chat row for that JID.
func (s *msgStore) hasChat(ctx context.Context, sessionID, jid string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM chats WHERE session_id = ? AND jid = ?`, sessionID, jid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// mergeLIDChat folds a hidden-LID chat into its phone-number twin: unread is
// summed, the twin's name is filled in when empty, messages that are not
// already present in the twin are moved over, and the old row is dropped.
func (s *msgStore) mergeLIDChat(ctx context.Context, sessionID, lid, pn string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO chats (session_id, jid, name, is_group, unread, updated_at)
		SELECT session_id, ?, name, is_group, unread, updated_at FROM chats WHERE session_id = ? AND jid = ?
		ON CONFLICT(session_id, jid) DO UPDATE SET
			unread = chats.unread + excluded.unread,
			updated_at = CASE WHEN excluded.updated_at > chats.updated_at THEN excluded.updated_at ELSE chats.updated_at END,
			name = CASE WHEN chats.name = '' THEN excluded.name ELSE chats.name END`,
		pn, sessionID, lid); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET chat_jid = ?
		WHERE session_id = ? AND chat_jid = ?
		  AND NOT EXISTS (SELECT 1 FROM messages m2 WHERE m2.session_id = messages.session_id
		                   AND m2.id = messages.id AND m2.chat_jid = ?)`,
		pn, sessionID, lid, pn); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM chats WHERE session_id = ? AND jid = ?`, sessionID, lid)
	return err
}

func (s *msgStore) getWebhook(ctx context.Context, sessionID string) (*WebhookConfig, error) {
	var enabled, byEvents int
	var url, eventsJSON string
	err := s.db.QueryRowContext(ctx, `SELECT enabled, url, webhook_by_events, events FROM webhooks WHERE session_id = ?`, sessionID).
		Scan(&enabled, &url, &byEvents, &eventsJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := &WebhookConfig{Enabled: enabled == 1, URL: url, WebhookByEvents: byEvents == 1}
	_ = json.Unmarshal([]byte(eventsJSON), &cfg.Events)
	return cfg, nil
}

func (s *msgStore) setWebhook(ctx context.Context, sessionID string, cfg *WebhookConfig) error {
	eventsJSON, _ := json.Marshal(cfg.Events)
	if cfg.Events == nil {
		eventsJSON = []byte("[]")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO webhooks (session_id, enabled, url, webhook_by_events, events)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET enabled = excluded.enabled, url = excluded.url,
			webhook_by_events = excluded.webhook_by_events, events = excluded.events`,
		sessionID, boolToInt(cfg.Enabled), cfg.URL, boolToInt(cfg.WebhookByEvents), string(eventsJSON))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
