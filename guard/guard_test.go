package guard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/hook"
)

// Stage 1 (P0): the mechanical hard-deny floor is now ON by default, even
// with no interceptor. Deleting a DB rule must not remove it (docs/02 §3.5).
// Benign commands still pass through; only critical intents are blocked.
func TestPreToolUsePassthrough(t *testing.T) {
	g := New()

	block := func(cmd string) bool {
		input, _ := json.Marshal(map[string]string{"command": cmd})
		b, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input)
		return b
	}

	for _, cmd := range []string{
		`curl https://acme.com/`,
		`ls -la`,
	} {
		if block(cmd) {
			t.Errorf("benign command should not be blocked, got block for %q", cmd)
		}
	}
	// audit still records every gated call
	if len(g.Audit()) == 0 {
		t.Error("audit should record gated calls")
	}
}

func TestPreToolUseHardDeny(t *testing.T) {
	g := New()
	block := func(cmd string) bool {
		input, _ := json.Marshal(map[string]string{"command": cmd})
		b, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input)
		return b
	}
	for _, cmd := range []string{
		`rm -rf /`,
		`rm -rf /*`,
		`mkfs.ext4 /dev/sda1`,
		`dd if=/dev/zero of=/dev/sda bs=1M`,
	} {
		if !block(cmd) {
			t.Errorf("critical intent should be hard-denied, got allow for %q", cmd)
		}
	}
	// Opt-out is explicit and test-only: production must not disable it.
	g2 := New(WithHardDeny(false))
	input, _ := json.Marshal(map[string]string{"command": "rm -rf /"})
	b, _, _ := g2.Hooks().PreToolUse(context.Background(), "Bash", input)
	if b {
		t.Errorf("WithHardDeny(false) should restore passthrough for tests")
	}
}

var _ = hook.PreToolUse
