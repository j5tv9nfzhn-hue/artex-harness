package harness

// Sprint 1.1 contract test: the streaming executor's FIFO + exclusivity rule is
// what makes "read-only parallel / write serial" real, not just a flag in the
// capability registry. These tests pin that behaviour at the SDK boundary so a
// future refactor cannot silently turn writes into parallel calls.
//
// They use streamExec directly (no provider, no network): deterministic and fast.

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// fakeTool is a CoreTool whose Call blocks on a release channel so the test can
// observe which tools were executing at the same time.
type fakeTool struct {
	name       string
	safe       bool
	started    chan struct{}
	release    chan struct{}
	startedN   *atomic.Int32
	maxOverlap *atomic.Int32
	live       *atomic.Int32
	mu         *sync.Mutex
}

func (f *fakeTool) Name() string                { return f.name }
func (f *fakeTool) Description() string         { return f.name }
func (f *fakeTool) Prompt() string              { return "" }
func (f *fakeTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (f *fakeTool) IsReadOnly(json.RawMessage) bool {
	return f.safe
}
func (f *fakeTool) IsConcurrencySafe(json.RawMessage) bool { return f.safe }
func (f *fakeTool) CheckPermissions(context.Context, json.RawMessage, permission.Context) permission.Decision {
	return permission.Allowed()
}
func (f *fakeTool) Call(ctx context.Context, _ json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
	f.startedN.Add(1)
	n := f.live.Add(1)
	for {
		old := f.maxOverlap.Load()
		if n <= old || f.maxOverlap.CompareAndSwap(old, n) {
			break
		}
	}
	select {
	case f.started <- struct{}{}:
	default:
	}
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	f.live.Add(-1)
	return actool.Result{Content: []llm.ContentBlock{llm.TextBlock("ok")}}, nil
}

// newTestLoop builds the minimal loop state streamExec.add needs.
func newTestLoop(tools ...actool.CoreTool) *loop {
	reg := actool.NewRegistry(tools...)
	return &loop{
		ctx:     context.Background(),
		toolCtx: context.Background(),
		in: QueryInput{
			Tools:          reg,
			MaxConcurrency: 10,
			PermissionMode: permission.ModeBypass,
		},
		// drain/drainSynthetic emit through yield. A no-op consumer is enough:
		// these tests assert scheduling and result counts, not event contents.
		yield: func(Event, error) bool { return true },
		sem:   make(chan struct{}, 10),
	}
}

func waitStarted(t *testing.T, ch <-chan struct{}, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("only %d/%d tools started within 2s", i, n)
		}
	}
}

func useBlock(id, name string) llm.ContentBlock {
	return llm.ContentBlock{Type: llm.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(`{}`)}
}

func TestStreamExecRunsSafeToolsInParallel(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var startedN, maxOverlap, live atomic.Int32
	var mu sync.Mutex
	var tools []actool.CoreTool
	for _, name := range []string{"read_a", "read_b", "read_c"} {
		tools = append(tools, &fakeTool{
			name: name, safe: true, started: started, release: release,
			startedN: &startedN, maxOverlap: &maxOverlap, live: &live, mu: &mu,
		})
	}
	e := newStreamExec(newTestLoop(tools...))
	for i, tl := range tools {
		e.add(useBlock("id_"+string(rune('a'+i)), tl.Name()))
	}
	waitStarted(t, started, 3)
	if got := maxOverlap.Load(); got < 2 {
		t.Fatalf("concurrency-safe tools serialised: max overlap = %d, want >= 2", got)
	}
	close(release)
}

func TestStreamExecGivesNonSafeToolExclusiveAccess(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var startedN, maxOverlap, live atomic.Int32
	var mu sync.Mutex
	read := &fakeTool{name: "read", safe: true, started: started, release: release,
		startedN: &startedN, maxOverlap: &maxOverlap, live: &live, mu: &mu}
	write := &fakeTool{name: "write", safe: false, started: started, release: release,
		startedN: &startedN, maxOverlap: &maxOverlap, live: &live, mu: &mu}
	e := newStreamExec(newTestLoop(read, write))
	// Order: read, write, read. The write must wait for the first read and must
	// not overlap the third read queued behind it.
	e.add(useBlock("id_1", "read"))
	e.add(useBlock("id_2", "write"))
	e.add(useBlock("id_3", "read"))
	waitStarted(t, started, 1)
	// Give the scheduler a chance to (wrongly) start the write and third read.
	time.Sleep(50 * time.Millisecond)
	if got := startedN.Load(); got != 1 {
		t.Fatalf("%d tools started while the exclusive tool was blocked behind a read, want 1", got)
	}
	close(release)
}

func TestStreamExecSynthesizesResultsOnCancel(t *testing.T) {
	// Regression anchor for "no orphan tool_use": every registered tool_use must
	// yield exactly one tool_result, even when the run is cancelled mid-flight.
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	var startedN, maxOverlap, live atomic.Int32
	var mu sync.Mutex
	blocking := &fakeTool{name: "blocking", safe: false, started: started,
		release: make(chan struct{}), startedN: &startedN, maxOverlap: &maxOverlap, live: &live, mu: &mu}
	l := newTestLoop(blocking)
	l.ctx, l.toolCtx = ctx, ctx
	e := newStreamExec(l)
	e.add(useBlock("id_1", "blocking"))
	waitStarted(t, started, 1)
	cancel()
	if _, aborted := e.finish(); !aborted {
		t.Fatal("finish must report the abort so the turn loop records why it stopped")
	}
	results := e.results()
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (no orphan tool_use)", len(results))
	}
	if !results[0].IsError {
		t.Fatal("the synthetic result must be an error so the model sees the interruption")
	}
}
