package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type SessionManager struct {
	appCtx    context.Context
	container *sqlstore.Container
	broker    *Broker
	store     *sessionStore
	msgs      *msgStore
	media     *mediaStore
	hooks     *webhookDispatcher
	waLogger  waLog.Logger
	log       *slog.Logger
	maxCalls  int
	recDir    string

	mu       sync.RWMutex
	sessions map[string]*Session
	order    []string
}

func newSessionManager(ctx context.Context, container *sqlstore.Container, broker *Broker, store *sessionStore, msgs *msgStore, media *mediaStore, hooks *webhookDispatcher, waLogger waLog.Logger, log *slog.Logger, maxCalls int) *SessionManager {
	return &SessionManager{
		appCtx:    ctx,
		container: container,
		broker:    broker,
		store:     store,
		msgs:      msgs,
		media:     media,
		hooks:     hooks,
		waLogger:  waLogger,
		log:       log,
		maxCalls:  maxCalls,
		sessions:  map[string]*Session{},
	}
}

func (m *SessionManager) register(s *Session) {
	m.mu.Lock()
	m.sessions[s.id] = s
	m.order = append(m.order, s.id)
	m.mu.Unlock()
}

func (m *SessionManager) unregister(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	for i, x := range m.order {
		if x == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
}

func (m *SessionManager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *SessionManager) ByInstance(name string) (*Session, bool) {
	if s, ok := m.Get(name); ok {
		return s, true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if s.info().Name == name {
			return s, true
		}
	}
	return nil, false
}

func (m *SessionManager) infos() []SessionInfo {
	m.mu.RLock()
	ordered := make([]*Session, 0, len(m.order))
	for _, id := range m.order {
		if s, ok := m.sessions[id]; ok {
			ordered = append(ordered, s)
		}
	}
	m.mu.RUnlock()
	out := make([]SessionInfo, 0, len(ordered))
	for _, s := range ordered {
		out = append(out, s.info())
	}
	return out
}

func (m *SessionManager) snapshotEvents() []any {
	evs := []any{map[string]any{"type": "session-list", "sessions": m.infos()}}
	m.mu.RLock()
	for _, id := range m.order {
		s, ok := m.sessions[id]
		if !ok {
			continue
		}
		s.mu.Lock()
		a := s.auth
		s.mu.Unlock()
		if a.QR != "" && !a.Paired {
			evs = append(evs, map[string]any{
				"type": "auth-state", "sessionId": id,
				"paired": false, "state": a.State, "qr": a.QR,
			})
		}
	}
	m.mu.RUnlock()
	return evs
}

// mergeDuplicateLIDChats folds any chat still keyed by a hidden LID into its
// phone-number twin, so a person who messaged under both addressing modes
// shows up as one chat instead of two. Runs at startup per session.
func (m *SessionManager) mergeDuplicateLIDChats(ctx context.Context, sessionID string) {
	lids, err := m.msgs.listLIDChats(ctx, sessionID)
	if err != nil {
		m.log.Warn("list lid chats failed", "session", sessionID, "err", err)
		return
	}
	for _, lidStr := range lids {
		lid, err := types.ParseJID(lidStr)
		if err != nil || lid.Server != types.HiddenUserServer {
			continue
		}
		pn, err := m.container.LIDMap.GetPNForLID(ctx, lid)
		if err != nil || pn.User == "" {
			m.log.Debug("no phone mapping for lid chat", "session", sessionID, "jid", lidStr)
			continue
		}
		if err := m.msgs.mergeLIDChat(ctx, sessionID, lidStr, pn.ToNonAD().String()); err != nil {
			m.log.Warn("merge lid chat failed", "session", sessionID, "jid", lidStr, "err", err)
			continue
		}
		m.log.Info("merged lid chat into phone chat", "session", sessionID, "lid", lidStr, "phone", pn.User)

		// A LID leaked into chat_jid as if it were a phone number (a call placed
		// from a LID chat did that) leaves a third, bogus chat behind: fold it in
		// as well, since its user part is the very LID resolved above.
		bogus := lid.User + "@" + types.DefaultUserServer
		if exists, err := m.msgs.hasChat(ctx, sessionID, bogus); err == nil && exists {
			if err := m.msgs.mergeLIDChat(ctx, sessionID, bogus, pn.ToNonAD().String()); err != nil {
				m.log.Warn("merge bogus number chat failed", "session", sessionID, "jid", bogus, "err", err)
				continue
			}
			m.log.Info("merged lid-as-number chat into phone chat", "session", sessionID, "jid", bogus, "phone", pn.User)
		}
	}
}

func (m *SessionManager) Restore(ctx context.Context) error {
	rows, err := m.store.list(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.JID == "" {
			_ = m.store.delete(ctx, row.ID)
			continue
		}
		jid, err := types.ParseJID(row.JID)
		if err != nil {
			m.log.Warn("dropping session with unparseable jid", "session", row.ID, "jid", row.JID)
			_ = m.store.delete(ctx, row.ID)
			continue
		}
		device, err := m.container.GetDevice(ctx, jid)
		if err != nil || device == nil {
			m.log.Warn("dropping session with no stored device", "session", row.ID, "jid", row.JID, "err", err)
			_ = m.store.delete(ctx, row.ID)
			continue
		}
		client := whatsmeow.NewClient(device, m.waLogger)
		s := newSession(m, row.ID, row.Name, client)
		m.register(s)
		if err := s.connect(ctx); err != nil {
			m.log.Error("session connect failed", "session", row.ID, "err", err)
		}
		m.mergeDuplicateLIDChats(ctx, row.ID)
	}
	m.broker.emitSessionList(m.infos())
	m.log.Info("sessions restored", "count", len(m.infos()))
	return nil
}

func (m *SessionManager) Create(name string) (string, error) {
	return m.CreateNamed(newSessionID(), name)
}

func (m *SessionManager) CreateNamed(id, name string) (string, error) {
	if _, exists := m.Get(id); exists {
		return "", fmt.Errorf("instance %s already exists", id)
	}
	if err := m.store.insert(m.appCtx, id, name); err != nil {
		return "", err
	}
	device := m.container.NewDevice()
	client := whatsmeow.NewClient(device, m.waLogger)
	s := newSession(m, id, name, client)
	m.register(s)
	m.broker.emitSessionList(m.infos())
	if err := s.startPairing(m.appCtx); err != nil {
		m.log.Error("start pairing failed", "session", id, "err", err)
		return "", fmt.Errorf("start pairing: %w", err)
	}
	m.log.Info("session created", "session", id, "name", name)
	return id, nil
}

func (m *SessionManager) Delete(ctx context.Context, id string) error {
	s, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	if s.client.Store.ID != nil {
		if err := s.client.Logout(ctx); err != nil {
			m.log.Warn("logout failed; deleting locally", "session", id, "err", err)
			_ = m.container.DeleteDevice(ctx, s.client.Store)
		}
	} else {
		s.client.Disconnect()
		_ = m.container.DeleteDevice(ctx, s.client.Store)
	}
	s.teardownAllCalls()
	m.unregister(id)
	_ = m.store.delete(ctx, id)
	m.broker.emitSessionList(m.infos())
	m.log.Info("session deleted", "session", id)
	return nil
}

// Rename changes the display name of a session (instance) in memory, in the
// database and on every connected panel through the session list event.
func (m *SessionManager) Rename(ctx context.Context, id, name string) error {
	s, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	if err := m.store.setName(ctx, id, name); err != nil {
		return err
	}
	s.mu.Lock()
	s.name = name
	s.mu.Unlock()
	m.broker.emitSessionList(m.infos())
	m.log.Info("session renamed", "session", id, "name", name)
	return nil
}

func (m *SessionManager) Logout(ctx context.Context, id string) error {
	s, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	if s.client.Store.ID != nil {
		if err := s.client.Logout(ctx); err != nil {
			m.log.Warn("logout failed", "session", id, "err", err)
		}
	}
	s.replaceClient(whatsmeow.NewClient(m.container.NewDevice(), m.waLogger))
	_ = m.store.setJID(ctx, id, "")
	s.setAuth(AuthSnapshot{State: "logged_out", Paired: false})
	m.log.Info("session disconnected", "session", id)
	return nil
}

func (m *SessionManager) Pair(id string) error {
	s, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	if s.client.Store.ID != nil {
		return fmt.Errorf("session already paired")
	}
	s.replaceClient(whatsmeow.NewClient(m.container.NewDevice(), m.waLogger))
	if err := s.startPairing(m.appCtx); err != nil {
		return fmt.Errorf("start pairing: %w", err)
	}
	m.broker.emitSessionList(m.infos())
	m.log.Info("session re-pairing", "session", id)
	return nil
}

func (m *SessionManager) disconnectAll() {
	m.mu.RLock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.RUnlock()
	for _, s := range all {
		s.shutdown()
	}
}
