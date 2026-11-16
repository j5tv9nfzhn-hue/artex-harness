package agent

// Sprint 3 regression tests for background delegation + the three-layer tool
// pool. The background path is the one that can silently break delegation
// pairing and the "no recursion" invariant, so both are pinned here with the
// fake provider (no network, deterministic).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/harness/delegation"
	actool "github.com/Autumn-27/norma/tool"
)

// waitFor polls fn until it returns true or the deadline passes.
func waitFor(t *testing.T, timeout time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// waitActivity drains emit-channel until one matches pred or the timeout hits.
// It blocks (does not busy-poll), so a goroutine that is still running cannot be
// mistaken for "no more events".
func waitActivity(t *testing.T, ch chan db.Activity, timeout time.Duration, pred func(db.Activity) bool) bool {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case a := <-ch:
			if pred(a) {
				return true
			}
		case <-deadline.C:
			return false
		}
	}
}

func TestBackgroundDelegationRunsAndNotifies(t *testing.T) {
	prov := &fakeSubagentProvider{}
	h := NewHarnessAgent(prov, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })

	var mu = make(chan db.Activity, 64)
	emit := func(a db.Activity) { mu <- a }

	tool := h.agentTool(nil, emit, 777)
	in := json.RawMessage(`{"agent_type":"recon","prompt":"enumerate hosts","run_in_background":true,"description":"recon hosts"}`)
	res, err := tool.Call(context.Background(), in, &actool.ToolContext{WorkingDir: t.TempDir()})
	if err != nil || res.IsError {
		t.Fatalf("background start failed: err=%v res=%s", err, resultText(res))
	}
	body := resultText(res)
	// The tool must return immediately with the delegation id, not the answer.
	if !strings.Contains(body, "后台子代理已启动") || !strings.Contains(body, "id=") {
		t.Fatalf("expected immediate background ack, got %q", body)
	}
	if strings.Contains(body, "recon done") {
		t.Fatal("background run leaked its result into the immediate return")
	}

	reg := delegation.Global.For("777", h.delegationOutputDir(777))
	ok := waitFor(t, 3*time.Second, func() bool {
		for _, r := range reg.List() {
			if r.Status.Terminal() {
				return true
			}
		}
		return false
	})
	if !ok {
		t.Fatal("background delegation never reached a terminal state")
	}
	// A paired tool_result activity must have been emitted.
	if !waitActivity(t, mu, 3*time.Second, func(a db.Activity) bool {
		return a.Kind == "tool_result" && strings.Contains(a.Summary, "子代理")
	}) {
		t.Fatal("background completion did not emit a paired tool_result")
	}
}

// TestBackgroundDelegationNotifierDeliversAndDrains pins the delivery contract
// that verification found broken: the completion must be handed to the parent
// (notifier), and only then removed from the registry. Before the fix the
// notifier was never wired, so `pending` stayed 1 forever and the parent model
// polled TaskList / slept instead of reading a result that already existed.
func TestBackgroundDelegationNotifierDeliversAndDrains(t *testing.T) {
	prov := &fakeSubagentProvider{}
	h := NewHarnessAgent(prov, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })

	delivered := make(chan string, 4)
	h.SetDelegationNotifier(func(taskID int64, text string) bool {
		delivered <- text
		return true
	})

	emit := func(db.Activity) {}
	tool := h.agentTool(nil, emit, 778)
	in := json.RawMessage(`{"agent_type":"recon","prompt":"enumerate hosts","run_in_background":true}`)
	if res, err := tool.Call(context.Background(), in, &actool.ToolContext{WorkingDir: t.TempDir()}); err != nil || res.IsError {
		t.Fatalf("background start failed: %v %s", err, resultText(res))
	}

	select {
	case text := <-delivered:
		if !strings.Contains(text, "<task-notification>") || !strings.Contains(text, "<status>done</status>") {
			t.Fatalf("notification text missing its contract fields: %q", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the completion was never handed to the parent (notifier not called)")
	}

	reg := delegation.Global.For("778", h.delegationOutputDir(778))
	if got := reg.PeekNotifications(); len(got) != 0 {
		t.Fatalf("notification was delivered but not drained: %d left", len(got))
	}
}

// A notifier that refuses (paused task, queue full) must NOT lose the result:
// the notification stays queued so a later resume can still deliver it.
func TestBackgroundDelegationKeepsNotificationWhenUndeliverable(t *testing.T) {
	prov := &fakeSubagentProvider{}
	h := NewHarnessAgent(prov, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })
	h.SetDelegationNotifier(func(int64, string) bool { return false })

	tool := h.agentTool(nil, func(db.Activity) {}, 779)
	in := json.RawMessage(`{"agent_type":"recon","prompt":"enumerate hosts","run_in_background":true}`)
	if res, err := tool.Call(context.Background(), in, &actool.ToolContext{WorkingDir: t.TempDir()}); err != nil || res.IsError {
		t.Fatalf("background start failed: %v %s", err, resultText(res))
	}

	reg := delegation.Global.For("779", h.delegationOutputDir(779))
	if !waitFor(t, 3*time.Second, func() bool {
		for _, r := range reg.List() {
			if r.Status.Terminal() {
				return true
			}
		}
		return false
	}) {
		t.Fatal("background delegation never reached a terminal state")
	}
	if got := reg.PeekNotifications(); len(got) == 0 {
		t.Fatal("an undelivered completion must remain queued, not be dropped")
	}
}

func TestBackgroundDelegationIsKilled(t *testing.T) {
	// A killed background run must reach 'killed' and emit a failure result.
	prov := &fakeSubagentProvider{}
	h := NewHarnessAgent(prov, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })
	ch := make(chan db.Activity, 64)
	tool := h.agentTool(nil, func(a db.Activity) { ch <- a }, 888)
	in := json.RawMessage(`{"agent_type":"recon","prompt":"slow recon","run_in_background":true}`)
	res, _ := tool.Call(context.Background(), in, &actool.ToolContext{WorkingDir: t.TempDir()})
	id := delegationIDFromResult(resultText(res))
	if id == "" {
		t.Fatalf("could not parse delegation id from %q", resultText(res))
	}
	reg := delegation.Global.For("888", h.delegationOutputDir(888))
	if !reg.Kill(id) {
		t.Fatalf("Kill(%s) returned false", id)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		r, ok := reg.Get(id)
		return ok && r.Status.Terminal()
	}) {
		t.Fatal("killed delegation never settled")
	}
	r, _ := reg.Get(id)
	if r.Status != delegation.StatusKilled && r.Status != delegation.StatusFailed {
		t.Fatalf("status = %q, want killed/failed", r.Status)
	}
	// Wait for the goroutine's finishDelegation to fully drain before returning,
	// so the deferred TempDir cleanup does not race the output write.
	waitActivity(t, ch, 2*time.Second, func(a db.Activity) bool {
		return a.Kind == "tool_result"
	})
}

func TestSynchronousDelegationStillReturnsInline(t *testing.T) {
	// Without run_in_background the answer must still come back inline — this is
	// the behaviour docs/08 pinned and we must not regress it.
	prov := &fakeSubagentProvider{}
	h := NewHarnessAgent(prov, "fake", t.TempDir(), nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })
	tool := h.agentTool(nil, nil, 999)
	in := json.RawMessage(`{"agent_type":"recon","prompt":"enumerate 127.0.0.1:8899"}`)
	res, err := tool.Call(context.Background(), in, &actool.ToolContext{WorkingDir: t.TempDir()})
	if err != nil || res.IsError {
		t.Fatalf("foreground delegation failed: %v", err)
	}
	if !strings.Contains(resultText(res), "recon done") {
		t.Fatalf("foreground result missing: %q", resultText(res))
	}
}

// Three-layer tool pool: "Agent" must never appear in any subagent's tools, so
// recursion is impossible by construction (depth guard is defence in depth).
func TestSubagentToolPoolExcludesAgent(t *testing.T) {
	h := NewHarnessAgent(&fakeSubagentProvider{}, "fake", t.TempDir(), nil, 64000, 4)
	// base with a fake MCP tool + an Agent tool to prove they are filtered.
	base := []actool.CoreTool{
		actool.Build(actool.Spec{Name: "mcp__browser__navigate"}),
		actool.Build(actool.Spec{Name: "TaskStop"}),
	}
	defs := h.agentDefinitions(base)
	for name, def := range defs {
		var hasAgent, hasTaskStop bool
		var hasMCP bool
		for _, toolName := range def.Tools {
			if toolName == "Agent" {
				hasAgent = true
			}
			if toolName == "TaskStop" {
				hasTaskStop = true
			}
			if strings.HasPrefix(toolName, "mcp__") {
				hasMCP = true
			}
		}
		if hasAgent {
			t.Errorf("%s subagent may delegate again (Agent present)", name)
		}
		if hasTaskStop {
			t.Errorf("%s subagent got a globally-denied tool TaskStop", name)
		}
		if !hasMCP {
			t.Errorf("%s subagent lost MCP access (layer 3 must always allow)", name)
		}
	}
}

func delegationIDFromResult(s string) string {
	const p = "id="
	i := strings.Index(s, p)
	if i < 0 {
		return ""
	}
	rest := s[i+len(p):]
	if j := strings.IndexAny(rest, " 。\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// A background delegation must run OUTSIDE the task workspace so it cannot
// clobber the operator's file tree / DIFF / checkpoints, and an empty isolation
// dir leaves no litter.
func TestBackgroundDelegationRunsIsolated(t *testing.T) {
	prov := &fakeSubagentProvider{}
	ws := t.TempDir()
	h := NewHarnessAgent(prov, "fake", ws, nil, 64000, 4)
	h.SetNonStreaming(func() bool { return true })
	ch := make(chan db.Activity, 64)
	tool := h.agentTool(nil, func(a db.Activity) { ch <- a }, 6001)
	in := json.RawMessage(`{"agent_type":"recon","prompt":"x","run_in_background":true}`)
	res, _ := tool.Call(context.Background(), in, &actool.ToolContext{WorkingDir: ws})
	id := delegationIDFromResult(resultText(res))
	reg := delegation.Global.For("6001", h.delegationOutputDir(6001))
	waitActivity(t, ch, 3*time.Second, func(a db.Activity) bool { return a.Kind == "tool_result" })

	rec, ok := reg.Get(id)
	if !ok {
		t.Fatal("delegation record missing after completion")
	}
	if rec.WorkDir == "" || rec.WorkDir == ws {
		t.Fatalf("background delegation must run in an isolated dir, got %q", rec.WorkDir)
	}
	if !strings.Contains(rec.WorkDir, filepath.Join("delegations", "6001")) {
		t.Fatalf("isolation dir not under the delegation store: %q", rec.WorkDir)
	}
	// Empty isolation dir is cleaned up (the fake provider writes no files).
	// Cleanup runs right after finishDelegation emits the tool_result, so poll
	// instead of assuming it already happened.
	if !waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(rec.WorkDir)
		return os.IsNotExist(err)
	}) {
		entries, _ := os.ReadDir(rec.WorkDir)
		if len(entries) == 0 {
			t.Fatal("an empty isolation dir should have been removed on cleanup")
		}
	}
}
