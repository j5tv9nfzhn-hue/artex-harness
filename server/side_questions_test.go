package server

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

type sideHTTPProvider struct {
	started chan llm.CompletionRequest
	release chan struct{}
	summary func(context.Context, llm.CompletionRequest) (llm.Message, llm.Usage, error)
}

func (p *sideHTTPProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(y func(llm.StreamEvent, error) bool) {
		p.started <- req
		if !y(llm.StreamEvent{Type: llm.SEMessageStart, Usage: llm.Usage{InputTokens: 19}}, nil) {
			return
		}
		if !y(llm.StreamEvent{Type: llm.SETextDelta, Text: "partial answer"}, nil) {
			return
		}
		select {
		case <-p.release:
			y(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
		case <-ctx.Done():
			y(llm.StreamEvent{}, ctx.Err())
		}
	}
}
func (p *sideHTTPProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	if req.Thinking == "disabled" && p.summary != nil {
		msg, usage, err := p.summary(ctx, req)
		return msg, "end_turn", usage, err
	}
	for _, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("atomic answer")}}, "end_turn", llm.Usage{InputTokens: 19}, nil
}

type sideHTTPFixture struct {
	s        *Server
	m        *Manager
	provider *sideHTTPProvider
	handler  http.Handler
	token    string
}

func newSideHTTPFixture(t *testing.T) *sideHTTPFixture {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{m: m, engine: NewEngine(m), ctx: ctx, jwtKey: []byte("btw-test-signing-key-only"), chatBusy: map[string]bool{}}
	s.initSideQuestions()
	p := &sideHTTPProvider{started: make(chan llm.CompletionRequest, 16), release: make(chan struct{})}
	cfg := agent.Config{Format: llm.FormatOpenAI, BaseURL: "http://fixture.invalid", Model: "fixture", Stream: true}
	s.cfgMu.Lock()
	s.llmCfg = cfg
	s.llmOn = true
	s.llmDirect = bindSideProvider(p, cfg, 0, "fixture")
	s.cfgMu.Unlock()
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	f := &sideHTTPFixture{s: s, m: m, provider: p, handler: s.Handler(), token: token}
	t.Cleanup(func() {
		cancel()
		for _, done := range s.cancelSideWhere(func(sidequestion.Parent) bool { return true }) {
			<-done
		}
		<-s.side.done
		s.flushSideSnapshots()
		m.Close()
	})
	return f
}

func (f *sideHTTPFixture) conversation(t *testing.T) (sidequestion.Parent, string) {
	t.Helper()
	c, err := f.m.pg.CreateConversation("mainagent", "side HTTP", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.m.pg.DeleteConversation(c.ID) })
	return sidequestion.Parent{ConversationID: c.ID}, fmt.Sprintf("/api/conversations/%d/side-questions", c.ID)
}
func (f *sideHTTPFixture) checkpoint(t *testing.T, p sidequestion.Parent) sidequestion.Snapshot {
	t.Helper()
	snap := sidequestion.Snapshot{Parent: p, RunID: time.Now().UnixNano(), Version: 1, CapturedAt: time.Now().UTC(), Model: sideModel(f.s.llmCfg, 0, "fixture"), Request: llm.CompletionRequest{MaxTokens: 128, Messages: []llm.Message{llm.UserText("main context marker")}}}
	if err := f.m.pg.SaveSideSnapshot(t.Context(), snap); err != nil {
		t.Fatal(err)
	}
	return snap
}

func waitSide(t *testing.T, d *db.DB, id, status string) *sidequestion.Exchange {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		e, err := d.SideRequest(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if e != nil && e.Status == status {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("side %s did not reach %s", id, status)
	return nil
}

// TestSideCheckpointPersistsBeforeAdmissionAndRestart: the main-run checkpoint
// is durable in the DB before admission, and a restart recovers the partial
// answer plus the snapshot (and can rebuild the request from stored context).
//
// 2026-11-21 S6-TRIM round 2: the side-questions HTTP endpoints that used to
// create/cancel a side request are gone, so this test no longer drives the
// question/answer round-trip over HTTP; it covers the checkpoint pipeline
// (Attach -> SideSnapshot -> restart recovery) that the runtime still uses.
func TestSideCheckpointPersistsBeforeAdmissionAndRestart(t *testing.T) {
	f := newSideHTTPFixture(t)
	p, _ := f.conversation(t)
	bound := bindSideProvider(f.provider, f.s.llmCfg, 0, "fixture")
	ctx, deps := sidequestion.Attach(f.s.ctx, p, harness.QueryDeps{}, bound)
	mainCtx, stopMain := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range deps.CallModel(mainCtx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("live checkpoint")}, MaxTokens: 128}) {
		}
	}()
	select {
	case <-f.provider.started:
	case <-time.After(time.Second):
		t.Fatal("main never started")
	}
	// Admission checkpoint: the runtime publisher captures the live main
	// context in memory; it reaches the DB on the flush ticker (250ms).
	// (The old HTTP test got it persisted for free via handleSideQuestions'
	// explicit SaveSideSnapshot; without the endpoint, wait for the flush.)
	var snap *sidequestion.Snapshot
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.s.flushSideSnapshots()
		snap, err = f.m.pg.SideSnapshot(t.Context(), p.Key())
		if err == nil && snap != nil && len(snap.Request.Messages) > 0 && snap.Request.Messages[0].Text() == "live checkpoint" {
			break
		}
		snap = nil
		time.Sleep(50 * time.Millisecond)
	}
	if snap == nil {
		t.Fatalf("admission checkpoint not durable %+v %v", snap, err)
	}
	stopMain()
	<-done
	// Simulate an abrupt process exit: persisted running row survives, memory doesn't.
	orphan, _, err := f.m.pg.StartSideRequest(t.Context(), *snap, "orphan", "restart")
	if err != nil {
		t.Fatal(err)
	}
	orphan.Answer = "saved before crash"
	orphan.Sequence = 1
	if _, err = f.m.pg.UpdateSideRequest(t.Context(), *orphan); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel := context.WithCancel(t.Context())
	defer cancel()
	restarted := &Server{m: f.m, ctx: ctx2, llmCfg: f.s.llmCfg, llmDirect: bound, llmOn: true}
	restarted.initSideQuestions()
	if got := waitSide(t, f.m.pg, orphan.ID, "interrupted"); got.Answer != "saved before crash" {
		t.Fatal("restart discarded partial answer")
	}
	restored, err := restarted.sideSnapshot(t.Context(), p)
	if err != nil || restored == nil {
		t.Fatal("restart missing snapshot")
	}
	if _, err = restarted.sideProvider(restored.Model); err != nil {
		t.Fatal(err)
	}
	if req, err := sidequestion.BuildRequest(*restored, nil, "continue after restart"); err != nil || req.Messages[0].Text() != "live checkpoint" {
		t.Fatal("restart cannot ask from stored context")
	}
}

func TestSideRejectsDeletedOrChangedCachedProfile(t *testing.T) {
	f := newSideHTTPFixture(t)
	p := &db.LLMProfile{Name: "btw-cached-profile", Format: "openai", BaseURL: "http://fixture.invalid", APIKey: "test-only", Model: "fixture", Streaming: true}
	id, err := f.m.pg.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.m.pg.DeleteProfile(id) })
	cfg, ok := f.s.loadProfileConfig(id)
	if !ok {
		t.Fatal("profile unavailable")
	}
	f.s.provByProfile = map[int64]*provEntry{id: {prov: f.provider, cfg: cfg}}
	model := sideModel(cfg, id, p.Name)
	if _, err = f.s.sideProvider(model); err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.pg.Exec(`UPDATE llm_profiles SET model='changed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.sideProvider(model); err == nil {
		t.Fatal("cached profile bypassed model identity check")
	}
	if err = f.m.pg.DeleteProfile(id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.sideProvider(model); err == nil {
		t.Fatal("deleted profile remained usable from cache")
	}
}

func TestSideRestoredWorkerRuntimePublishesNewCheckpoint(t *testing.T) {
	f := newSideHTTPFixture(t)
	task, err := f.m.CreateTask("restore side publisher", "context", nil, 3600, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { _ = f.m.pg.DeleteTask(id) })
	if err := f.m.pg.SetPaused(id, true); err != nil {
		t.Fatal(err)
	}
	iid, err := task.Store.AddNode(db.KindIntent, map[string]any{"summary": "restored worker"}, 1, "paused", "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the production startup path, including the deadline coordinator
	// which registers the shared runtime before the worker loops.
	f.s.restoreTaskRuntimes()
	t.Cleanup(func() {
		for _, task := range f.m.List() {
			f.s.engine.StopTask(task.ID)
		}
	})
	f.s.engine.runtimeMu.Lock()
	runtime := f.s.engine.runtimes[task.ID]
	f.s.engine.runtimeMu.Unlock()
	if runtime == nil {
		t.Fatal("restored task runtime missing")
	}
	p := sidequestion.Parent{TaskID: id, ExplorationID: task.ExpID, IntentID: iid}
	close(f.provider.release)
	bound := bindSideProvider(f.provider, f.s.llmCfg, 0, "fixture")
	ctx, deps := sidequestion.Attach(runtime.ctx, p, harness.QueryDeps{}, bound)
	_, _, _, err = deps.CallModelSync(ctx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("after restart context")}})
	if err != nil {
		t.Fatal(err)
	}
	f.s.flushSideSnapshots()
	snap, err := f.m.pg.SideSnapshot(t.Context(), p.Key())
	if err != nil || snap == nil || snap.Request.Messages[0].Text() != "after restart context" {
		t.Fatalf("restored runtime lost checkpoint publisher: %+v %v", snap, err)
	}
}
