package agent

import (
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func mkNameTools(names ...string) []actool.CoreTool {
	out := make([]actool.CoreTool, 0, len(names))
	for _, n := range names {
		out = append(out, actool.Build(actool.Spec{Name: n}))
	}
	return out
}

// The Manager owns the browser stack; per-run MCP augmentation adds the same tool
// names again. Exactly one instance of each must survive, and it must be the
// Manager's. The previous implementation dropped every mcp__browser__/
// mcp__chrome-devtools__ name from the assembled list — the Manager's own tools
// included — so the agent ran with no browser and no DevTools tools at all while
// the panel reported both servers live.
func TestPreferManagedBrowserToolsKeepsOneManagedCopy(t *testing.T) {
	managed := mkNameTools("mcp__browser__navigate", "mcp__chrome-devtools__list_console_messages")
	assembled := mkNameTools("Bash", "graph_overview",
		"mcp__browser__navigate", "mcp__chrome-devtools__list_console_messages",
		"mcp__browser__navigate", // a second augment copy
	)

	got := preferManagedBrowserTools(assembled, managed)

	count := map[string]int{}
	for _, tl := range got {
		count[tl.Name()]++
	}
	if count["Bash"] != 1 || count["graph_overview"] != 1 {
		t.Fatalf("built-in tools must survive untouched, got %v", count)
	}
	if count["mcp__browser__navigate"] != 1 {
		t.Fatalf("want exactly one navigate, got %d (%v)", count["mcp__browser__navigate"], count)
	}
	if count["mcp__chrome-devtools__list_console_messages"] != 1 {
		t.Fatalf("want exactly one list_console_messages, got %d (%v)", count["mcp__chrome-devtools__list_console_messages"], count)
	}
	// The survivor must be the Manager's own instance, not the augment copy.
	if got[0] != managed[0] || got[1] != managed[1] {
		t.Fatalf("managed instances must be the ones kept")
	}
}

// Without a Manager (browserTools unwired) the assembled set passes through: the
// per-run MCP path is still the only browser source in that configuration.
func TestPreferManagedBrowserToolsNoManager(t *testing.T) {
	assembled := mkNameTools("Bash", "mcp__browser__navigate")
	got := preferManagedBrowserTools(assembled, nil)
	if len(got) != len(assembled) {
		t.Fatalf("want unchanged length, got %d vs %d", len(got), len(assembled))
	}
}
