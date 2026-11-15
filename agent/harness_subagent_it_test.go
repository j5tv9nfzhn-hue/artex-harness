package agent

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

// fakeSubagentProvider answers every completion with one final text, so a subagent
// finishes on its first turn. No tools, no network.
type fakeSubagentProvider struct{ calls int }

// Stream emits a minimal, valid text response. Subagents always stream (norma's
// subagent.Definition has no NonStreaming switch), so the fake must speak the stream
// protocol rather than the non-streaming one.
func (p *fakeSubagentProvider) Stream(ctx context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		p.calls++
		// Stay "in flight" long enough for the parent cancellation to land.
		select {
		case <-time.After(80 * time.Millisecond):
		case <-ctx.Done():
			yield(llm.StreamEvent{}, ctx.Err())
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "recon done: 2 endpoints"}, nil) {
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil) {
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}

func (p *fakeSubagentProvider) Complete(ctx context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.calls++
	// Emulate a slow-ish subagent so the parent cancellation lands mid-run.
	select {
	case <-time.After(60 * time.Millisecond):
	case <-ctx.Done():
		return llm.Message{}, "", llm.Usage{}, ctx.Err()
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("recon done: 2 endpoints")}}, "stop", llm.Usage{}, nil
}

// TestAgentToolSurvivesParentCancel is the integration-level regression test for
// docs/08 §1: run the REAL Agent tool, cancel the parent mid-delegation, and require
// the subagent to still return its answer.
func TestAgentToolSurvivesParentCancel(t *testing.T) {
	prov := &fakeSubagentProvider{}
	h := NewHarnessAgent(prov, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })

	tool := h.agentTool(nil, nil, 1)
	parent, parentCancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond) // cancel while the child is in flight
		parentCancel()
	}()

	in := json.RawMessage(`{"agent_type":"recon","prompt":"enumerate 127.0.0.1:8899"}`)
	tc := &actool.ToolContext{WorkingDir: t.TempDir()}
	res, err := tool.Call(parent, in, tc)

	if err != nil {
		t.Fatalf("delegation failed after parent cancel: %v", err)
	}
	if res.IsError {
		t.Fatalf("subagent reported an error: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "recon done") {
		t.Fatalf("subagent answer missing: %q", resultText(res))
	}
	if prov.calls == 0 {
		t.Fatal("provider was never called — delegation did not run")
	}
}

// TestAgentToolRefusesToStartAfterCancel: the detached context is for runs already in
// flight; a canceled turn must not START new delegated work.
func TestAgentToolRefusesToStartAfterCancel(t *testing.T) {
	prov := &fakeSubagentProvider{}
	h := NewHarnessAgent(prov, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })
	tool := h.agentTool(nil, nil, 1)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	in := json.RawMessage(`{"agent_type":"recon","prompt":"x"}`)
	res, err := tool.Call(canceled, in, &actool.ToolContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(res), "未启动子代理") {
		t.Fatalf("expected a refusal result, got %q (isError=%v)", resultText(res), res.IsError)
	}
	if prov.calls != 0 {
		t.Fatal("provider must not be called for a canceled turn")
	}
}

// TestAgentToolRejectsUnknownType keeps the definition map honest.
func TestAgentToolRejectsUnknownType(t *testing.T) {
	h := NewHarnessAgent(&fakeSubagentProvider{}, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })
	tool := h.agentTool(nil, nil, 1)
	res, err := tool.Call(context.Background(), json.RawMessage(`{"agent_type":"nope","prompt":"x"}`),
		&actool.ToolContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(res), "unknown agent_type") {
		t.Fatalf("got %q", resultText(res))
	}
}

func resultText(r actool.Result) string {
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}
