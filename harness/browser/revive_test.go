package browser

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

// A browser transport that dies mid-run used to poison the rest of the turn: the
// agent kept calling tool values bound to the dead pipe, so one killed Chrome meant
// "write |1: file already closed" on every navigate/tabs/snapshot/close until the
// run ended (task 214 burned ~15 turns that way). The wrapper now revives the server
// once and retries the same call.
func TestBrokenCallRetriesOnRevivedServer(t *testing.T) {
	var calls, launches int
	launcher := func(_ context.Context, server string) (Conn, error) {
		launches++
		gen := launches
		tool := actool.Build(actool.Spec{
			Name: "mcp__fake__act",
			Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
				calls++
				if gen == 1 {
					return actool.Result{}, errors.New("write |1: file already closed")
				}
				return actool.Result{Content: []llm.ContentBlock{llm.TextBlock("ok")}}, nil
			},
		})
		return &fakeConn{name: server, tools: []actool.CoreTool{tool}}, nil
	}

	cfg := DefaultConfig()
	cfg.Servers = []string{"browser"}
	cfg.StartTimeout = 2 * time.Second
	m := New(cfg, launcher, nil)

	tools, err := m.Tools(context.Background(), 1)
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v (%d)", err, len(tools))
	}
	res, err := tools[0].Call(context.Background(), json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("want the revived server to serve the call, got %v", err)
	}
	if res.IsError {
		t.Fatalf("want a successful result, got error result")
	}
	if calls != 2 {
		t.Fatalf("want exactly one retry (2 calls), got %d", calls)
	}
	if launches != 2 {
		t.Fatalf("want the server relaunched once, got %d launches", launches)
	}
}

// If the revived server cannot serve the same tool, the original error is returned
// rather than a misleading success or a second failure mode.
func TestBrokenCallSurfacesOriginalErrorWhenReviveFails(t *testing.T) {
	var launches int
	launcher := func(_ context.Context, server string) (Conn, error) {
		launches++
		tool := actool.Build(actool.Spec{
			Name: "mcp__fake__act",
			Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
				return actool.Result{}, errors.New("pipe gone")
			},
		})
		return &fakeConn{name: server, tools: []actool.CoreTool{tool}}, nil
	}
	cfg := DefaultConfig()
	cfg.Servers = []string{"browser"}
	cfg.StartTimeout = 2 * time.Second
	m := New(cfg, launcher, nil)

	tools, err := m.Tools(context.Background(), 2)
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v", err)
	}
	if _, err := tools[0].Call(context.Background(), json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("want the failure to surface, got nil")
	}
	if launches < 2 {
		t.Fatalf("want at least one revive attempt, got %d launches", launches)
	}
}
