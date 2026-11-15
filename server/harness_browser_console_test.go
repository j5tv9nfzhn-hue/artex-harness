package server

import (
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// consoleErrors must lift only warn/error lines out of the MCP blob, and it must
// distinguish "no errors" from "could not parse" — an empty list that silently
// hides a parser failure is the one failure mode the panel cannot afford.
func TestConsoleErrorsExtractsLevelsAndFlagsUnparsed(t *testing.T) {
	now := time.Now()
	env := `{"content":[{"type":"text","text":"## Console messages\nmsgid=1 [info] boot ok\nmsgid=2 [error] Uncaught TypeError: x is not a function\nmsgid=3 [warn] deprecated api\nmsgid=4 [error] Uncaught TypeError: x is not a function\n"}]}`
	acts := []db.Activity{
		{ID: 1, Kind: "tool_result", Tool: "mcp__chrome-devtools__list_console_messages", CreatedAt: now},
		{ID: 2, Kind: "tool_result", Tool: "mcp__chrome-devtools__list_console_messages", CreatedAt: now}, // empty blob
		{ID: 3, Kind: "tool_use", Tool: "mcp__chrome-devtools__list_console_messages", CreatedAt: now},       // wrong kind
		{ID: 4, Kind: "tool_result", Tool: "mcp__browser__navigate", CreatedAt: now},                          // not devtools
	}
	detail := func(id int64) (string, error) {
		if id == 1 {
			return env, nil
		}
		return "", nil
	}
	got, unparsed := consoleErrors(acts, detail, 20)
	if len(got) != 2 {
		t.Fatalf("want 2 deduped entries (info ignored), got %d: %+v", len(got), got)
	}
	if got[0].Level != "error" || !strings.Contains(got[0].Text, "TypeError") {
		t.Fatalf("first entry wrong: %+v", got[0])
	}
	if got[1].Level != "warn" {
		t.Fatalf("want warn second, got %+v", got[1])
	}
	if unparsed != 1 {
		t.Fatalf("want 1 unparsed (empty blob), got %d", unparsed)
	}
}

// Plain (non-JSON) tool output must still work: the MCP server has shipped both
// shapes, and the line-based scan is what makes that survivable.
func TestConsoleErrorsAcceptsPlainText(t *testing.T) {
	acts := []db.Activity{{ID: 7, Kind: "tool_result", Tool: "mcp__chrome-devtools__get_console_message", CreatedAt: time.Now()}}
	got, unparsed := consoleErrors(acts, func(int64) (string, error) {
		return "[fatal] page crashed", nil
	}, 5)
	if len(got) != 1 || got[0].Level != "error" || unparsed != 0 {
		t.Fatalf("want one error entry and no unparsed, got %+v unparsed=%d", got, unparsed)
	}
}
