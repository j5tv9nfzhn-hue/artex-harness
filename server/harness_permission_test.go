package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hperm "github.com/Autumn-27/artex/harness/permission"
)

// The permission endpoints must stay policy-only: they may not widen the SDK
// mode or bypass the hooks. The default is ModeBypass at the SDK layer with no
// new rules, so a fresh server introduces no behaviour change.
func TestHarnessPermissionDefaults(t *testing.T) {
	s := &Server{}
	p := s.harnessPermission()
	if got := p.Snapshot().Mode; got != hperm.ModeBypass {
		t.Fatalf("default mode = %q, want bypass", got)
	}
	if d := p.Evaluate("Bash", false); d.Behavior != hperm.BehaviorAllow || d.Reason != hperm.ReasonMode {
		t.Fatalf("default decision = %+v, want allow/mode (hooks remain the gate)", d)
	}
}

func TestHarnessPermissionReplaceAndGrant(t *testing.T) {
	s := &Server{}
	body := `{"mode":"default","rules":[{"name":"ask-bash","pattern":"Bash","behavior":"ask","priority":10,"enabled":true}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/harness/permission", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.harnessPermissionReplace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	p := s.harnessPermission()
	if d := p.Evaluate("Bash", false); d.Behavior != hperm.BehaviorAsk {
		t.Fatalf("after PUT, Bash should ask: %+v", d)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/harness/permission/grant?tool=Bash", nil)
	rec = httptest.NewRecorder()
	s.harnessPermissionGrant(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST grant = %d: %s", rec.Code, rec.Body.String())
	}
	if d := p.Evaluate("Bash", false); d.Behavior != hperm.BehaviorAllow || d.Reason != hperm.ReasonSession {
		t.Fatalf("session grant not applied: %+v", d)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/harness/permission", nil)
	rec = httptest.NewRecorder()
	s.harnessPermissionView(rec, req)
	var view struct {
		Policy        hperm.Config `json:"policy"`
		SessionGrants []string     `json:"session_grants"`
		VetoPoint     string       `json:"veto_point"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Policy.Mode != hperm.ModeDefault || len(view.Policy.Rules) != 1 {
		t.Fatalf("policy view = %+v", view.Policy)
	}
	if len(view.SessionGrants) != 1 || view.SessionGrants[0] != "Bash" {
		t.Fatalf("session_grants = %v", view.SessionGrants)
	}
	if !strings.Contains(view.VetoPoint, "hooks") {
		t.Fatalf("veto_point must state that hooks remain the gate: %q", view.VetoPoint)
	}
}

func TestHarnessPermissionRejectsBadInput(t *testing.T) {
	s := &Server{}
	for _, body := range []string{`{"mode":"bogus"}`, `not-json`} {
		req := httptest.NewRequest(http.MethodPut, "/api/harness/permission", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.harnessPermissionReplace(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT %q = %d, want 400", body, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/harness/permission/grant", nil)
	rec := httptest.NewRecorder()
	s.harnessPermissionGrant(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("grant without tool = %d, want 400", rec.Code)
	}
}
