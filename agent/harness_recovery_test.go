package agent

// Sprint 1.3 — the HARNESS conversational runner must recover from a truncated
// final answer instead of surfacing half a conclusion. The SDK owns the recovery
// loop; this test pins that OUR runner actually enables it (a false here means
// the wiring regressed, even if the SDK tests still pass).

import (
	"testing"

	"github.com/Autumn-27/norma/agentcore"
)

func TestHarnessEnablesMaxTokensRecovery(t *testing.T) {
	h := NewHarnessAgent(nil, "", t.TempDir(), nil, 0, 40)
	var opts agentcore.Options
	h.modelRunConfig().ApplyModelDefaults(&opts)
	if !opts.EscalateMaxTokens {
		t.Fatal("HARNESS must enable max-output-tokens escalation + resume recovery")
	}
}

// The connection-test path is a one-shot "ping" and must NOT escalate: doing so
// would turn a cheap connectivity check into a resume loop (see provider.go).
func TestZeroConfigDoesNotEscalate(t *testing.T) {
	var opts agentcore.Options
	ModelRunConfig{}.ApplyModelDefaults(&opts)
	if opts.EscalateMaxTokens {
		t.Fatal("a zero config must not enable escalation")
	}
}
