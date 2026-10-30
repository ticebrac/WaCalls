package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// crmLead is the CRM's view of a contact: the collaborator who claimed it
// (owner) and the instance used to talk to it (session). Leads are claimed on
// first touch and never change owner, so a conversation stays with the person
// who opened it.
type crmLead struct {
	Phone     string `json:"phone"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	SessionID string `json:"sessionId"`
	CreatedAt int64  `json:"createdAt"`
}

type crmStore struct{ db *qDB }

func newCrmStore(ctx context.Context, db *qDB) (*crmStore, error) {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS crm_leads (
		phone      TEXT PRIMARY KEY,
		name       TEXT NOT NULL DEFAULT '',
		owner      TEXT NOT NULL,
		session_id TEXT NOT NULL,
		created_at BIGINT NOT NULL DEFAULT 0
	)`); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS crm_origins (
		user_id    TEXT PRIMARY KEY,
		session_id TEXT NOT NULL
	)`); err != nil {
		return nil, err
	}
	return &crmStore{db: db}, nil
}

func (s *crmStore) getLead(ctx context.Context, phone string) (*crmLead, error) {
	row := s.db.QueryRowContext(ctx, `SELECT phone, name, owner, session_id, created_at FROM crm_leads WHERE phone = ?`, phone)
	var l crmLead
	if err := row.Scan(&l.Phone, &l.Name, &l.Owner, &l.SessionID, &l.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// claimLead registers the lead for its first owner and returns the stored row.
// A lead already claimed by someone else comes back untouched so the caller
// can reject the request.
func (s *crmStore) claimLead(ctx context.Context, phone, name, owner, sessionID string) (*crmLead, error) {
	existing, err := s.getLead(ctx, phone)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Name == "" && name != "" {
			if _, err := s.db.ExecContext(ctx, `UPDATE crm_leads SET name = ? WHERE phone = ? AND owner = ?`, name, phone, owner); err == nil {
				existing.Name = name
			}
		}
		return existing, nil
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO crm_leads (phone, name, owner, session_id, created_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (phone) DO NOTHING`,
		phone, name, owner, sessionID, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	return s.getLead(ctx, phone)
}

func (s *crmStore) listLeads(ctx context.Context, owner string) ([]crmLead, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT phone, name, owner, session_id, created_at
		FROM crm_leads WHERE owner = ? ORDER BY created_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []crmLead{}
	for rows.Next() {
		var l crmLead
		if err := rows.Scan(&l.Phone, &l.Name, &l.Owner, &l.SessionID, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// listCalls returns the call log rows this collaborator originated.
func (s *crmStore) listCalls(ctx context.Context, owner string, limit int) ([]CallLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+callLogColumns+` FROM calls
		WHERE owner = ? ORDER BY started_at DESC LIMIT ?`, owner, limit)
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

// deleteLead drops the claim on a phone number so it can be reassigned (or
// cleaned up after a mistaken import).
func (s *crmStore) deleteLead(ctx context.Context, phone string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM crm_leads WHERE phone = ?`, phone)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ownsChat reports whether a chat belongs to a lead claimed by owner. It is the
// gate that keeps one collaborator out of another's conversations.
func (s *crmStore) ownsChat(ctx context.Context, owner, chatJID string) bool {
	phone := chatJID
	if i := strings.Index(chatJID, "@"); i >= 0 {
		phone = chatJID[:i]
	}
	lead, err := s.getLead(ctx, phone)
	return err == nil && lead != nil && lead.Owner == owner
}

func (s *crmStore) getOrigin(ctx context.Context, userID string) (string, error) {
	var sid string
	err := s.db.QueryRowContext(ctx, `SELECT session_id FROM crm_origins WHERE user_id = ?`, userID).Scan(&sid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return sid, nil
}

func (s *crmStore) setOrigin(ctx context.Context, userID, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO crm_origins (user_id, session_id) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET session_id = excluded.session_id`, userID, sessionID)
	return err
}

func (s *crmStore) clearOrigin(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM crm_origins WHERE user_id = ?`, userID)
	return err
}
