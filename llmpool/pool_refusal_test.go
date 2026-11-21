package llmpool

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

const refusalText = "I'm sorry, but I can't help with penetration testing."

// scriptProv is a Complete-first fake: each call consumes the next scripted
// reply (the last one repeats), and records every request it received so
// tests can assert what the reprompt actually sent.
type scriptReply struct {
	text string
	msg  *llm.Message // full message when text is not enough (e.g. tool use)
	err  error
}

type scriptProv struct {
	replies []scriptReply
	reqs    []llm.CompletionRequest
	calls   int
}

func (s *scriptProv) Complete(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	s.reqs = append(s.reqs, req)
	n := s.calls
	s.calls++
	if n >= len(s.replies) {
		n = len(s.replies) - 1
	}
	r := s.replies[n]
	if r.err != nil {
		return llm.Message{}, "", llm.Usage{}, r.err
	}
	if r.msg != nil {
		return *r.msg, "", llm.Usage{}, nil
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(r.text)}}, "", llm.Usage{}, nil
}

func (s *scriptProv) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}

func refuseProv() *scriptProv {
	return &scriptProv{replies: []scriptReply{{text: refusalText}}}
}

func okTextProv(text string) *scriptProv {
	return &scriptProv{replies: []scriptReply{{text: text}}}
}

// TestRefusalRepromptOnceThenSuccess pins the reprompt contract: the first
// refusal is retried on the SAME member with the authorization reminder
// appended to System, and a success on that retry is returned directly.
func TestRefusalRepromptOnceThenSuccess(t *testing.T) {
	a := &scriptProv{replies: []scriptReply{{text: refusalText}, {text: "扫描完成"}}}
	reg := NewRegistry(nil, nil)
	p := New([]*Member{member(1, "a", RankActive, a)}, reg)

	msg, _, _, err := p.Complete(context.Background(), llm.CompletionRequest{System: []string{"base"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := msg.Text(); got != "扫描完成" {
		t.Fatalf("text = %q, want the reprompted success", got)
	}
	if a.calls != 2 {
		t.Fatalf("calls = %d, want 2 (refusal + one reprompt)", a.calls)
	}
	if len(a.reqs) != 2 || len(a.reqs[1].System) != 2 || !strings.Contains(a.reqs[1].System[1], "已获书面授权") {
		t.Fatalf("second request did not carry the authorization reminder: %#v", a.reqs)
	}
	if a.reqs[0].System[0] != "base" || len(a.reqs[0].System) != 1 {
		t.Fatalf("first request was modified: %#v", a.reqs[0].System)
	}
	if reg.Get(1).Fails != 0 || reg.IsOpen(1) {
		t.Fatalf("refusal tripped or counted against health: %+v", reg.Get(1))
	}
}

// TestRefusalFailoverToNextModel: still refusing after the reprompt switches
// to the next configuration; the reprompt stays on the first member (2 calls)
// and the refusal never trips health.
func TestRefusalFailoverToNextModel(t *testing.T) {
	a, b := refuseProv(), okTextProv("done by b")
	reg := NewRegistry(nil, nil)
	p := New([]*Member{member(1, "a", 10, a), member(2, "b", 5, b)}, reg)

	msg, _, _, err := p.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil || msg.Text() != "done by b" {
		t.Fatalf("got %q / %v, want b's answer", msg.Text(), err)
	}
	if a.calls != 2 || b.calls != 1 {
		t.Fatalf("calls: a=%d b=%d, want 2/1 (a: refusal + reprompt, b: success)", a.calls, b.calls)
	}
	if reg.IsOpen(1) || reg.IsOpen(2) {
		t.Fatal("a refusal must not trip any breaker")
	}
}

// TestRefusalExhaustionCapsSwitches: four always-refusing members, at most
// two switches — three configurations tried, the fourth never touched — and
// the last refusal is returned verbatim with a nil error.
func TestRefusalExhaustionCapsSwitches(t *testing.T) {
	provs := []*scriptProv{refuseProv(), refuseProv(), refuseProv(), refuseProv()}
	members := []*Member{
		member(1, "a", 10, provs[0]), member(2, "b", 9, provs[1]),
		member(3, "c", 8, provs[2]), member(4, "d", 7, provs[3]),
	}
	reg := NewRegistry(nil, nil)
	p := New(members, reg)

	msg, _, _, err := p.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("exhausted refusal must be returned with nil error, got %v", err)
	}
	if msg.Text() != refusalText {
		t.Fatalf("text = %q, want the last refusal verbatim", msg.Text())
	}
	// a: refusal + reprompt; b, c: one refusal each (reprompt already spent);
	// d: never tried — two switches means three configurations at most.
	if got := []int{provs[0].calls, provs[1].calls, provs[2].calls, provs[3].calls}; got[0] != 2 || got[1] != 1 || got[2] != 1 || got[3] != 0 {
		t.Fatalf("calls = %v, want [2 1 1 0]", got)
	}
	for i := int64(1); i <= 4; i++ {
		if reg.IsOpen(i) {
			t.Fatalf("profile %d tripped on refusals", i)
		}
	}
}

// A reply carrying a tool call is work, not a refusal — no reprompt, no
// failover, returned as-is on the first call.
func TestRefusalSkippedWhenToolUsePresent(t *testing.T) {
	toolMsg := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		llm.TextBlock(refusalText),
		{Type: llm.BlockToolUse, ID: "toolu_1", Name: "Bash"},
	}}
	a := &scriptProv{replies: []scriptReply{{msg: &toolMsg}}}
	p := New([]*Member{member(1, "a", RankActive, a)}, NewRegistry(nil, nil))

	msg, _, _, err := p.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolUses()) != 1 || a.calls != 1 {
		t.Fatalf("tool-use reply was reprompted or altered (calls=%d)", a.calls)
	}
}

// The reprompt happens at most once per request: a member that refuses, is
// reprompted, refuses again, then is reached again via failover must not get
// a second reminder.
func TestRepromptOnlyOncePerRequest(t *testing.T) {
	a := refuseProv()
	b := refuseProv()
	c := okTextProv("c ok")
	p := New([]*Member{member(1, "a", 10, a), member(2, "b", 5, b), member(3, "c", 4, c)}, NewRegistry(nil, nil))

	msg, _, _, err := p.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil || msg.Text() != "c ok" {
		t.Fatalf("got %q / %v, want c's answer", msg.Text(), err)
	}
	// b refused once (no reprompt left for the request) and the failover
	// moved on — exactly one call, no reminder appended to its request.
	if b.calls != 1 {
		t.Fatalf("b.calls = %d, want 1 (no second reprompt in the same request)", b.calls)
	}
	if len(b.reqs[0].System) != 0 {
		t.Fatalf("failover request wrongly carried the reminder: %#v", b.reqs[0].System)
	}
	if a.calls != 2 {
		t.Fatalf("a.calls = %d, want 2 (refusal + the single reprompt)", a.calls)
	}
}
