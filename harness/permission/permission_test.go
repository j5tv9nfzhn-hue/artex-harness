package permission

import (
	"testing"

	acperm "github.com/Autumn-27/norma/permission"
)

func TestParseModeAndToSDK(t *testing.T) {
	for _, m := range ValidModes {
		got, err := ParseMode(string(m))
		if err != nil || got != m {
			t.Fatalf("ParseMode(%q) = %q, %v", m, got, err)
		}
	}
	if _, err := ParseMode("superuser"); err == nil {
		t.Fatal("an unknown mode must be rejected")
	}
	if Mode("nonsense").ToSDK() != acperm.ModeDefault {
		t.Fatal("an unknown mode must fail closed to default, never widen")
	}
	if ModePlan.ToSDK() != acperm.ModePlan {
		t.Fatal("mode mapping drifted")
	}
}

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig("")
	if err != nil || c.Mode != ModeBypass || len(c.Rules) != 0 {
		t.Fatalf("empty config = %+v, %v", c, err)
	}
	if _, err := ParseConfig(`{"mode":"nope"}`); err == nil {
		t.Fatal("a bad persisted mode must be rejected, not silently defaulted")
	}
	if _, err := ParseConfig(`{"rules":[{"name":"r","behavior":"maybe"}]}`); err == nil {
		t.Fatal("a bad rule behavior must be rejected")
	}
	c, err = ParseConfig(`{"mode":"plan","rules":[{"name":"ask-bash","pattern":"Bash","behavior":"ask","priority":10,"enabled":true}]}`)
	if err != nil || c.Mode != ModePlan || len(c.Rules) != 1 {
		t.Fatalf("ParseConfig = %+v, %v", c, err)
	}
}

func TestEvaluateRulePriorityAndDenyWins(t *testing.T) {
	p := NewPolicy(Config{Mode: ModeBypass, Rules: []Rule{
		{Name: "allow-all", Pattern: "*", Behavior: BehaviorAllow, Priority: 1, Enabled: true},
		{Name: "deny-bash", Pattern: "Bash(rm -rf:*)", Behavior: BehaviorDeny, Priority: 100, Enabled: true},
	}})
	d := p.Evaluate("Bash", false)
	if d.Behavior != BehaviorDeny || d.Reason != ReasonDenyRule || d.Rule != "deny-bash" {
		t.Fatalf("deny rule must win over bypass and lower-priority allow: %+v", d)
	}

	p = NewPolicy(Config{Mode: ModeBypass, Rules: []Rule{
		{Name: "disabled", Pattern: "Bash", Behavior: BehaviorDeny, Priority: 100, Enabled: false},
	}})
	if d := p.Evaluate("Bash", false); d.Behavior != BehaviorAllow || d.Reason != ReasonMode {
		t.Fatalf("a disabled rule must not fire: %+v", d)
	}
}

func TestEvaluateAskAndSessionGrant(t *testing.T) {
	p := NewPolicy(Config{Mode: ModeDefault, Rules: []Rule{
		{Name: "ask-bash", Pattern: "Bash", Behavior: BehaviorAsk, Priority: 10, Enabled: true},
	}})
	if d := p.Evaluate("Bash", false); d.Behavior != BehaviorAsk || d.Reason != ReasonAskRule {
		t.Fatalf("expected an ask: %+v", d)
	}
	p.GrantSession("Bash")
	if d := p.Evaluate("Bash", false); d.Behavior != BehaviorAllow || d.Reason != ReasonSession {
		t.Fatalf("session grant should allow: %+v", d)
	}
	// A grant is session-scoped: replacing the policy clears it.
	if err := p.Replace(Config{Mode: ModeDefault, Rules: []Rule{
		{Name: "ask-bash", Pattern: "Bash", Behavior: BehaviorAsk, Priority: 10, Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if d := p.Evaluate("Bash", false); d.Behavior != BehaviorAsk {
		t.Fatalf("Replace must clear stale grants: %+v", d)
	}
	grants := p.SessionGrants()
	if len(grants) != 0 {
		t.Fatalf("session grants = %v, want empty", grants)
	}
}

func TestEvaluateReadOnlyAndWildcard(t *testing.T) {
	p := NewPolicy(Config{Mode: ModeDefault})
	if d := p.Evaluate("Read", true); d.Behavior != BehaviorAllow || d.Reason != ReasonReadOnly {
		t.Fatalf("read-only tools should not ask: %+v", d)
	}
	if d := p.Evaluate("Write", false); d.Behavior != BehaviorAllow || d.Reason != ReasonNoRule {
		t.Fatalf("no rule should not introduce a new ask: %+v", d)
	}
	p = NewPolicy(Config{Mode: ModeDefault, Rules: []Rule{
		{Name: "ask-any", Pattern: "*", Behavior: BehaviorAsk, Enabled: true},
	}})
	if d := p.Evaluate("Read", true); d.Behavior != BehaviorAsk {
		t.Fatalf("an explicit wildcard ask rule must be honoured: %+v", d)
	}
}

func TestReplaceRejectsBadInput(t *testing.T) {
	p := NewPolicy(DefaultConfig())
	if err := p.Replace(Config{Mode: "bogus"}); err == nil {
		t.Fatal("Replace must reject an unknown mode")
	}
	if err := p.Replace(Config{Mode: ModePlan, Rules: []Rule{{Name: "x", Behavior: "maybe"}}}); err == nil {
		t.Fatal("Replace must reject an invalid behavior")
	}
	// A rejected Replace must not have mutated the live policy.
	if p.Snapshot().Mode != ModeBypass {
		t.Fatal("a failed Replace must leave the policy untouched")
	}
}
