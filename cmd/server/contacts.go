package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// contactContent maps a shared contact to the stored message shape:
// Text = display name, FileName = phone number (digits, from the vCard TEL line).
func contactContent(displayName, vcard string) extractedMessage {
	return extractedMessage{Type: "contact", Text: displayName, FileName: phoneFromVCard(vcard)}
}

func phoneFromVCard(vcard string) string {
	for _, line := range strings.Split(vcard, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "TEL") {
			continue
		}
		if i := strings.LastIndex(line, ":"); i >= 0 {
			return normalizePhone(line[i+1:])
		}
	}
	return ""
}

func buildVCard(name, phone string) string {
	digits := withDefaultCountry(normalizePhone(phone))
	safe := strings.NewReplacer("\n", " ", ";", " ").Replace(name)
	return "BEGIN:VCARD\nVERSION:3.0\nN:;" + safe + ";;;\nFN:" + safe +
		"\nTEL;type=CELL;waid=" + digits + ":+" + digits + "\nEND:VCARD"
}

func (s *Session) sendContactMessage(ctx context.Context, peer types.JID, name, phone string) (*whatsmeow.SendResponse, error) {
	vcard := buildVCard(name, phone)
	resp, err := s.client.SendMessage(ctx, peer, &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String(name),
			Vcard:       proto.String(vcard),
		},
	})
	if err != nil {
		return nil, err
	}
	stored := &StoredMessage{
		ID:        string(resp.ID),
		SessionID: s.id,
		ChatJID:   peer.String(),
		SenderJID: s.jidString(),
		FromMe:    true,
		Type:      "contact",
		Text:      name,
		FileName:  withDefaultCountry(normalizePhone(phone)),
		Status:    "PENDING",
		Timestamp: resp.Timestamp.UnixMilli(),
	}
	if stored.Timestamp == 0 {
		stored.Timestamp = time.Now().UnixMilli()
	}
	if err := s.mgr.msgs.insertMessage(ctx, stored); err != nil {
		s.log.Warn("persist outgoing contact failed", "err", err)
	}
	s.mgr.broker.emitMessage(stored)
	return &resp, nil
}

func (s *server) handleSendContact(w http.ResponseWriter, r *http.Request) {
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
		Name    string `json:"name"`
		Phone   string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Phone) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and phone required"})
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
	resp, err := sess.sendContactMessage(r.Context(), sess.canonicalJIDFromInput(r.Context(), target), strings.TrimSpace(body.Name), body.Phone)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": string(resp.ID), "timestamp": resp.Timestamp.UnixMilli()})
}
