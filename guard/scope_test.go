package guard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestCheckHostAgainstScopes(t *testing.T) {
	scopes := []db.TaskScope{
		{Kind: "root_domain", Domain: "example.com"},
		{Kind: "subdomain", Domain: "api.example.com"},
		{Kind: "cidr", Net: "10.0.0.0/24"},
		{Kind: "ip", Net: "192.0.2.10/32"},
	}
	if !CheckHostAgainstScopes("example.com", scopes) {
		t.Error("root domain exact should match")
	}
	if !CheckHostAgainstScopes("sub.example.com", scopes) {
		t.Error("subdomain of root should match")
	}
	if !CheckHostAgainstScopes("api.example.com", scopes) {
		t.Error("subdomain exact should match")
	}
	if CheckHostAgainstScopes("evil.com", scopes) {
		t.Error("evil.com should not match")
	}
	if !CheckHostAgainstScopes("10.0.0.5", scopes) {
		t.Error("10.0.0.5 in 10.0.0.0/24 should match")
	}
	if CheckHostAgainstScopes("10.0.1.5", scopes) {
		t.Error("10.0.1.5 should not match")
	}
	if !CheckHostAgainstScopes("192.0.2.10", scopes) {
		t.Error("192.0.2.10/32 should match")
	}
}

func TestExtractHosts(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"command": "curl https://api.example.com/v1 && curl http://10.0.0.5:8080/"})
	hosts := ExtractHosts("Bash", input)
	if len(hosts) == 0 {
		t.Fatal("expected hosts")
	}
	found := map[string]bool{}
	for _, h := range hosts {
		found[h] = true
	}
	if !found["api.example.com"] {
		t.Errorf("missing api.example.com in %v", hosts)
	}
	if !found["10.0.0.5"] {
		t.Errorf("missing 10.0.0.5 in %v", hosts)
	}
}

func TestCheckScopeOutOfScope(t *testing.T) {
	checker := &StaticScopeChecker{ScopesByTask: map[int64][]db.TaskScope{
		1: {{Kind: "root_domain", Domain: "example.com"}},
	}}
	input, _ := json.Marshal(map[string]string{"url": "https://evil.com/x"})
	dec := CheckScope(context.Background(), checker, 1, "WebFetch", input)
	if !dec.Checked || !dec.HasScope || dec.InScope {
		t.Errorf("expected out-of-scope deny decision, got %+v", dec)
	}
	input2, _ := json.Marshal(map[string]string{"url": "https://sub.example.com/x"})
	dec2 := CheckScope(context.Background(), checker, 1, "WebFetch", input2)
	if !dec2.Checked || !dec2.InScope {
		t.Errorf("expected in-scope, got %+v", dec2)
	}
}

func TestCheckScopeUnanchoredFailOpen(t *testing.T) {
	checker := &StaticScopeChecker{ScopesByTask: map[int64][]db.TaskScope{}}
	input, _ := json.Marshal(map[string]string{"url": "https://evil.com/x"})
	dec := CheckScope(context.Background(), checker, 1, "WebFetch", input)
	if !dec.Checked || dec.HasScope {
		t.Errorf("unanchored task should be Checked=true HasScope=false, got %+v", dec)
	}
	// Guard-level: unanchored must not block (fail open by design).
}
