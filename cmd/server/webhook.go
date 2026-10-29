package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	EvMessagesUpsert   = "MESSAGES_UPSERT"
	EvMessagesUpdate   = "MESSAGES_UPDATE"
	EvSendMessage      = "SEND_MESSAGE"
	EvConnectionUpdate = "CONNECTION_UPDATE"
	EvQrCodeUpdated    = "QRCODE_UPDATED"
	EvPresenceUpdate   = "PRESENCE_UPDATE"
	EvChatsUpsert      = "CHATS_UPSERT"
	EvChatsUpdate      = "CHATS_UPDATE"
	EvGroupsUpsert     = "GROUPS_UPSERT"
	EvGroupsUpdate     = "GROUPS_UPDATE"
)

type webhookDispatcher struct {
	store  *msgStore
	client *http.Client
}

func newWebhookDispatcher(store *msgStore) *webhookDispatcher {
	return &webhookDispatcher{
		store:  store,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (d *webhookDispatcher) enabledEvent(cfg *WebhookConfig, event string) bool {
	if cfg == nil || !cfg.Enabled || cfg.URL == "" {
		return false
	}
	if !cfg.WebhookByEvents || len(cfg.Events) == 0 {
		return true
	}
	for _, e := range cfg.Events {
		if strings.EqualFold(e, event) {
			return true
		}
	}
	return false
}

func (d *webhookDispatcher) dispatch(ctx context.Context, sessionID, instanceName, event string, data any) {
	cfg, err := d.store.getWebhook(ctx, sessionID)
	if err != nil || !d.enabledEvent(cfg, event) {
		return
	}
	payload := map[string]any{
		"event":       event,
		"instance":    instanceName,
		"destination": cfg.URL,
		"date_time":   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"sender":      "wacalls",
		"data":        data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	go func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, cfg.URL, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if resp, err := d.client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
}
