package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type server struct {
	broker    *Broker
	sessions  *SessionManager
	log       *slog.Logger
	staticDir string
	apiKey    string
	media     *mediaStore
	crm       *crmStore
}

func newServer(ctx context.Context, dbTarget, staticDir, dataDir, apiKey string, maxCalls int, log *slog.Logger) (*server, error) {
	database, err := openDatabase(dbTarget)
	if err != nil {
		return nil, err
	}
	container := sqlstore.NewWithDB(database.db, sqlstoreDialect(database), waLog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		return nil, err
	}
	store, err := newSessionStore(ctx, database)
	if err != nil {
		return nil, err
	}
	msgs, err := newMsgStore(ctx, database)
	if err != nil {
		return nil, err
	}
	crm, err := newCrmStore(ctx, database)
	if err != nil {
		return nil, err
	}
	media, err := newMediaStore(dataDir + "/media")
	if err != nil {
		return nil, err
	}
	hooks := newWebhookDispatcher(msgs)

	waLogger := waLog.Noop
	if log.Enabled(ctx, slog.LevelDebug) {
		// DEBUG (not INFO) so the raw <call>/<ack> wire exchange is visible;
		// that is the only way to see why WhatsApp rejects an offer.
		waLogger = waLog.Stdout("WA", "DEBUG", true)
	}

	broker := NewBroker()
	mgr := newSessionManager(ctx, container, broker, store, msgs, media, hooks, waLogger, log, maxCalls)
	mgr.recDir = filepath.Join(dataDir, "recordings")
	broker.SnapshotFn = mgr.snapshotEvents
	broker.OnCallEnded = mgr.handleCallEnded

	return &server{broker: broker, sessions: mgr, log: log, staticDir: staticDir, apiKey: apiKey, media: media, crm: crm}, nil
}
