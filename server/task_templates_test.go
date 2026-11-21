package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConversationPatchReturnsPinState(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
	conversation, err := m.pg.CreateConversation("mainagent", "pin through http", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.DeleteConversation(conversation.ID) })
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"title": "  pinned title  ", "pinned": true})
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/conversations/%d", conversation.ID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated struct {
		Title    string  `json:"title"`
		Pinned   bool    `json:"pinned"`
		PinnedAt *string `json:"pinned_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Title != "pinned title" || !updated.Pinned || updated.PinnedAt == nil {
		t.Fatalf("unexpected conversation patch response: %+v", updated)
	}

	body, _ = json.Marshal(map[string]any{"title": strings.Repeat("会", maxConversationTitleRunes+1)})
	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/conversations/%d", conversation.ID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("overlong conversation title status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]any{"title": strings.Repeat("x", maxConversationRequestBytes+1)})
	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/conversations/%d", conversation.ID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized conversation patch status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]any{
		"agent_key": "mainagent", "title": strings.Repeat("会", maxConversationTitleRunes+1),
	})
	req = httptest.NewRequest(http.MethodPost, "/api/conversations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("overlong conversation create status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]any{
		"agent_key": "mainagent", "title": strings.Repeat("x", maxConversationRequestBytes+1),
	})
	req = httptest.NewRequest(http.MethodPost, "/api/conversations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized conversation create status=%d body=%s", rec.Code, rec.Body.String())
	}
}
