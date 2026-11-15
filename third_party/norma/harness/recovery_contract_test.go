package harness

// Sprint 1.3 contract tests for the recovery loop. These pin the recovery paths
// at the SDK boundary with a scripted model (no network, no provider).
//
// Design reference: mature harnesses treat "resume after truncation" and
// "reactive compaction" as first-class loop transitions, not errors.

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func truncatedTurn(text string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.SETextDelta, Text: text},
		{Type: llm.SEMessageDelta, StopReason: "max_tokens"},
		{Type: llm.SEMessageStop},
	}
}

func recoveryInput() QueryInput {
	return QueryInput{
		Messages:          []llm.Message{llm.UserText("go")},
		Tools:             tool.NewRegistry(),
		PermissionMode:    permission.ModeBypass,
		EscalateMaxTokens: true,
	}
}

func testDeps(m *scriptedModel) QueryDeps { return QueryDeps{CallModel: m.call} }

func TestMaxTokensRecoveryResumesThenCompletes(t *testing.T) {
	m := &scriptedModel{turns: [][]llm.StreamEvent{
		truncatedTurn("partial answer"),
		truncatedTurn("continued"),
		textTurn("final answer"),
	}}
	term := drain(t, recoveryInput(), testDeps(m))
	if term.Reason != ReasonCompleted {
		t.Fatalf("reason = %q, want completed", term.Reason)
	}
	if term.Text != "final answer" {
		t.Fatalf("terminal text = %q, want the completed answer", term.Text)
	}
	if m.calls != 3 {
		t.Fatalf("model calls = %d, want 3 (truncate + resume + complete)", m.calls)
	}
}

// Escalate-once + resume(3×) + the truncated turn that finally surfaces = 5
// model calls. The point of this test is the hard bound: a provider that always
// truncates must not spin forever.
func TestMaxTokensRecoveryIsBounded(t *testing.T) {
	var turns [][]llm.StreamEvent
	for i := 0; i < 20; i++ {
		turns = append(turns, truncatedTurn("still truncated"))
	}
	m := &scriptedModel{turns: turns}
	term := drain(t, recoveryInput(), testDeps(m))
	if m.calls != 5 {
		t.Fatalf("model calls = %d, want exactly 5 (1 escalate + 3 nudges + final)", m.calls)
	}
	if term == nil || term.Reason != ReasonCompleted {
		t.Fatalf("terminal = %+v, want the truncated answer surfaced as completion", term)
	}
}

func TestRecoveryInjectsContinueNudge(t *testing.T) {
	m := &scriptedModel{turns: [][]llm.StreamEvent{
		truncatedTurn("partial"),
		truncatedTurn("continued"), // consumes the escalate retry, then a nudge is injected
		textTurn("done"),
	}}
	term := drain(t, recoveryInput(), testDeps(m))
	var found bool
	for _, msg := range term.Messages {
		for _, b := range msg.Content {
			if strings.Contains(b.Text, "Resume directly") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("resume nudge missing from the transcript (replay cannot explain the extra turn)")
	}
}

// A reactive compaction is attempted exactly once on prompt-too-long; a second
// overflow must surface as ReasonModelError rather than looping.
func TestReactiveCompactIsSingleShot(t *testing.T) {
	comp := &recoveryCompactor{}
	in := recoveryInput()
	in.Compactor = comp
	overflow := func(_ context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
		return func(yield func(llm.StreamEvent, error) bool) {
			yield(llm.StreamEvent{}, errOverflow{})
		}
	}
	term := drain(t, in, QueryDeps{CallModel: overflow})
	if term.Reason != ReasonModelError {
		t.Fatalf("reason = %q, want model_error (second overflow must not be retried)", term.Reason)
	}
	if comp.reactiveCalls != 1 {
		t.Fatalf("reactive compaction called %d times, want exactly 1", comp.reactiveCalls)
	}
}

// recoveryCompactor reports every stream error as an overflow and accepts the
// first reactive compaction, so the loop retries once and then must give up.
type recoveryCompactor struct{ reactiveCalls int }

func (c *recoveryCompactor) Pre(_ context.Context, msgs []llm.Message, _ int) []llm.Message {
	return msgs
}
func (c *recoveryCompactor) Reactive(_ context.Context, msgs []llm.Message) ([]llm.Message, bool) {
	c.reactiveCalls++
	return msgs, true
}
func (c *recoveryCompactor) IsOverflow(error) bool { return true }

type errOverflow struct{}

func (errOverflow) Error() string { return "prompt is too long" }
