package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func newTestCrm(t *testing.T) *crmStore {
	t.Helper()
	db, err := openDatabase(filepath.Join(t.TempDir(), "crm_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	crm, err := newCrmStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return crm
}

// A lead belongs to whoever claims it first; later claims (by the same person
// or anyone else) never move it to another session or owner.
func TestCrmLeadClaimOwnership(t *testing.T) {
	ctx := context.Background()
	crm := newTestCrm(t)

	lead, err := crm.claimLead(ctx, "5541999998888", "Paulo", "user-a", "sess-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if lead == nil || lead.Owner != "user-a" || lead.SessionID != "sess-1" || lead.Name != "Paulo" {
		t.Fatalf("first claim = %+v", lead)
	}

	// Same collaborator, new origin: the lead keeps the instance it started on.
	again, err := crm.claimLead(ctx, "5541999998888", "", "user-a", "sess-2")
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if again.SessionID != "sess-1" || again.Owner != "user-a" || again.Name != "Paulo" {
		t.Fatalf("re-claim mutated the lead: %+v", again)
	}

	// Someone else's claim comes back with the original owner attached.
	other, err := crm.claimLead(ctx, "5541999998888", "Intruso", "user-b", "sess-2")
	if err != nil {
		t.Fatalf("foreign claim: %v", err)
	}
	if other.Owner != "user-a" {
		t.Fatalf("lead changed hands: %+v", other)
	}
	if other.Name == "Intruso" {
		t.Fatalf("foreign claim renamed the lead: %+v", other)
	}

	// Listing is scoped to the owner.
	mine, err := crm.listLeads(ctx, "user-a")
	if err != nil || len(mine) != 1 {
		t.Fatalf("listLeads(user-a) = %v, %v", mine, err)
	}
	theirs, err := crm.listLeads(ctx, "user-b")
	if err != nil || len(theirs) != 0 {
		t.Fatalf("listLeads(user-b) = %v, %v", theirs, err)
	}
}

func TestCrmOriginRoundtrip(t *testing.T) {
	ctx := context.Background()
	crm := newTestCrm(t)

	if sid, err := crm.getOrigin(ctx, "user-a"); err != nil || sid != "" {
		t.Fatalf("initial origin = %q, %v", sid, err)
	}
	if err := crm.setOrigin(ctx, "user-a", "sess-1"); err != nil {
		t.Fatalf("setOrigin: %v", err)
	}
	if sid, _ := crm.getOrigin(ctx, "user-a"); sid != "sess-1" {
		t.Fatalf("origin = %q, want sess-1", sid)
	}
	if err := crm.setOrigin(ctx, "user-a", "sess-2"); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	if sid, _ := crm.getOrigin(ctx, "user-a"); sid != "sess-2" {
		t.Fatalf("origin after reassign = %q", sid)
	}
	if err := crm.clearOrigin(ctx, "user-a"); err != nil {
		t.Fatalf("clearOrigin: %v", err)
	}
	if sid, _ := crm.getOrigin(ctx, "user-a"); sid != "" {
		t.Fatalf("origin after clear = %q", sid)
	}
	// Origins are per collaborator.
	if err := crm.setOrigin(ctx, "user-b", "sess-9"); err != nil {
		t.Fatalf("setOrigin user-b: %v", err)
	}
	if sid, _ := crm.getOrigin(ctx, "user-a"); sid != "" {
		t.Fatalf("user-a origin leaked into user-b's: %q", sid)
	}
}

func TestCrmAuth(t *testing.T) {
	calls := 0
	handler := (&server{apiKey: "secret"}).crmAuth(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if crmUserID(r) != "u1" {
			t.Errorf("crmUserID = %q, want u1", crmUserID(r))
		}
		w.WriteHeader(http.StatusOK)
	})

	run := func(headers map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/crm/status", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}

	if code := run(nil); code != http.StatusForbidden {
		t.Fatalf("missing key = %d, want 403", code)
	}
	if code := run(map[string]string{"apikey": "wrong", "X-User-Id": "u1"}); code != http.StatusForbidden {
		t.Fatalf("wrong key = %d, want 403", code)
	}
	if code := run(map[string]string{"apikey": "secret"}); code != http.StatusBadRequest {
		t.Fatalf("missing user = %d, want 400", code)
	}
	if code := run(map[string]string{"Authorization": "Bearer secret", "X-User-Id": "u1"}); code != http.StatusOK {
		t.Fatalf("bearer = %d, want 200", code)
	}
	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1", calls)
	}

	// Without a configured key the whole CRM API reports itself as disabled.
	disabled := (&server{}).crmAuth(func(http.ResponseWriter, *http.Request) { t.Error("handler must not run") })
	req := httptest.NewRequest(http.MethodGet, "/api/crm/status", nil)
	req.Header.Set("apikey", "anything")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	disabled(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled = %d, want 503", rec.Code)
	}
}

func TestCrmEventFilter(t *testing.T) {
	ctx := context.Background()
	crm := newTestCrm(t)
	if _, err := crm.claimLead(ctx, "5541999998888", "", "user-a", "sess-1"); err != nil {
		t.Fatal(err)
	}
	s := &server{crm: crm}
	encode := func(v any) []byte {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	mine := "user-a"
	other := "user-b"

	cases := []struct {
		name string
		ev   map[string]any
		want bool
	}{
		{"session list kept", map[string]any{"type": "session-list"}, true},
		{"own lead message kept", map[string]any{
			"type": "message", "message": map[string]any{"chatJid": "5541999998888@s.whatsapp.net"},
		}, true},
		{"foreign lead message dropped", map[string]any{
			"type": "message", "message": map[string]any{"chatJid": "5541111111111@s.whatsapp.net"},
		}, false},
		{"own call status kept", map[string]any{"type": "call-status", "owner": mine}, true},
		{"foreign call status dropped", map[string]any{"type": "call-status", "owner": other}, false},
		{"presence dropped", map[string]any{"type": "presence"}, false},
	}
	for _, tc := range cases {
		if _, keep := s.crmEventFor(ctx, "user-a", encode(tc.ev)); keep != tc.want {
			t.Errorf("%s: keep = %v, want %v", tc.name, keep, tc.want)
		}
	}

	// call-list is rewritten down to the calls this collaborator owns.
	data, keep := s.crmEventFor(ctx, "user-a", encode(map[string]any{
		"type": "call-list",
		"calls": []CallRecord{
			{CallID: "1", Owner: &mine},
			{CallID: "2", Owner: &other},
			{CallID: "3", Owner: nil},
		},
	}))
	if !keep {
		t.Fatal("call-list dropped")
	}
	var got struct {
		Calls []CallRecord `json:"calls"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Calls) != 1 || got.Calls[0].CallID != "1" {
		t.Fatalf("filtered calls = %+v, want only call 1", got.Calls)
	}
}
