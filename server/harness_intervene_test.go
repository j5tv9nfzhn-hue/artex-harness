package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeSteerMessage(t *testing.T) {
	if _, err := normalizeSteerMessage(""); err == nil {
		t.Fatal("empty message must be rejected")
	}
	if _, err := normalizeSteerMessage("   \n\t "); err == nil {
		t.Fatal("whitespace-only message must be rejected")
	}
	got, err := normalizeSteerMessage("  先别爆破 /admin  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "先别爆破 /admin" {
		t.Fatalf("message not trimmed: %q", got)
	}
	// Runes, not bytes: a 4000-character CJK message must fit exactly.
	max := strings.Repeat("测", maxSteerMessageRunes)
	if _, err := normalizeSteerMessage(max); err != nil {
		t.Fatalf("a message of exactly %d runes must be accepted: %v", maxSteerMessageRunes, err)
	}
	if _, err := normalizeSteerMessage(max + "测"); err == nil {
		t.Fatal("oversized message must be rejected")
	}
}

// TestInterventionsIndexListsEveryPrimitive keeps the console's capability
// discovery honest: the UI renders whatever this endpoint returns, so a primitive
// that exists as a route but is missing here is invisible to the operator.
func TestInterventionsIndexListsEveryPrimitive(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.interventionsIndex(rec, httptest.NewRequest("GET", "/api/harness/interventions", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Primitives []struct {
			Name, Method, Path, Note string
		} `json:"primitives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"steer", "kill", "edit-intent", "order", "replan", "pause-all"}
	got := map[string]string{}
	for _, p := range body.Primitives {
		got[p.Name] = p.Path
	}
	for _, w := range want {
		path, ok := got[w]
		if !ok {
			t.Errorf("primitive %q not advertised by /api/harness/interventions", w)
			continue
		}
		if !strings.HasPrefix(path, "/api/tasks") {
			t.Errorf("primitive %q has unexpected path %q", w, path)
		}
	}
	if len(body.Primitives) != len(want) {
		t.Errorf("advertised %d primitives, expected %d", len(body.Primitives), len(want))
	}
}
