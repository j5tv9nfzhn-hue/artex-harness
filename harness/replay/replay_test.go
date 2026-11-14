package replay

import (
	"strings"
	"testing"
	"time"
)

func ev(rev, act int64, kind, tool, summary string, isErr bool) Event {
	return Event{
		Revision: rev, ActivityID: act, Kind: kind, Tool: tool,
		Summary: summary, IsError: isErr, At: time.Unix(act, 0),
	}
}

func TestBuildGroupsSortsAndDedupes(t *testing.T) {
	events := []Event{
		ev(2, 20, "text", "", "second turn", false),
		ev(1, 11, "tool_use", "Bash", "nmap", false),
		ev(1, 10, "text", "", "first turn", false),
		ev(1, 10, "text", "", "first turn", false), // duplicate row
		ev(0, 5, "round", "", "legacy prefix", false),
	}
	run := Build("7", 42, events)

	if len(run.Turns) != 3 {
		t.Fatalf("turns = %d, want 3 (legacy + rev1 + rev2)", len(run.Turns))
	}
	if run.Turns[0].Revision != 0 || run.Turns[1].Revision != 1 || run.Turns[2].Revision != 2 {
		t.Fatalf("turn order = %d,%d,%d", run.Turns[0].Revision, run.Turns[1].Revision, run.Turns[2].Revision)
	}
	if got := len(run.Turns[1].Items); got != 2 {
		t.Fatalf("rev 1 items = %d, want 2 after dedupe", got)
	}
	// Within a turn, items are ordered by activity id (the record order).
	if run.Turns[1].Items[0].ActivityID != 10 || run.Turns[1].Items[1].ActivityID != 11 {
		t.Fatalf("items not ordered by id: %+v", run.Turns[1].Items)
	}
	if run.ItemCount() != 4 {
		t.Fatalf("item count = %d, want 4", run.ItemCount())
	}
	// The legacy prefix is kept, not dropped.
	if run.Turns[0].Items[0].Summary != "legacy prefix" {
		t.Fatalf("legacy item lost: %+v", run.Turns[0])
	}
}

func TestSetTurnMetaAddsBoundaryAndKeepsOrder(t *testing.T) {
	run := Build("1", 1, []Event{ev(3, 30, "text", "", "x", false)})
	run.SetTurnMeta(3, "work", "work#9", "第 9 号意图", time.Unix(30, 0))
	run.SetTurnMeta(1, "planner", "planner#1", "第 1 轮规划", time.Unix(10, 0)) // no items
	if len(run.Turns) != 2 {
		t.Fatalf("turns = %d, want 2 (an empty round is still an event)", len(run.Turns))
	}
	if run.Turns[0].Revision != 1 || run.Turns[0].Kind != "planner" {
		t.Fatalf("boundary-only turn = %+v", run.Turns[0])
	}
	if run.Turns[1].Kind != "work" || run.Turns[1].Ref != "work#9" {
		t.Fatalf("meta not attached: %+v", run.Turns[1])
	}
}

func TestRenderMarkdownIsDeterministic(t *testing.T) {
	events := []Event{
		ev(1, 1, "text", "", "hello\nworld", false),
		ev(1, 2, "tool_use", "Bash", "ls", false),
		ev(1, 3, "tool_result", "Bash", "a\nb", true),
	}
	a := RenderMarkdown(Build("5", 9, events))
	b := RenderMarkdown(Build("5", 9, append([]Event{}, events...)))
	if a != b {
		t.Fatalf("render not deterministic:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	if !strings.Contains(a, "rev 1") || !strings.Contains(a, "(error)") {
		t.Fatalf("render missing expected content:\n%s", a)
	}
	// Newlines inside an item must be folded so one item stays one line.
	if strings.Contains(a, "hello\nworld") {
		t.Fatalf("multi-line summary not folded:\n%s", a)
	}
}

func TestRunHashChangesWithContent(t *testing.T) {
	h1 := Build("1", 1, []Event{ev(1, 1, "text", "", "a", false)}).Hash()
	h2 := Build("1", 1, []Event{ev(1, 1, "text", "", "b", false)}).Hash()
	if h1 == h2 {
		t.Fatal("hash must change when content changes")
	}
	if h1 != Build("1", 1, []Event{ev(1, 1, "text", "", "a", false)}).Hash() {
		t.Fatal("hash must be stable for identical content")
	}
}

func TestTruncationIsReported(t *testing.T) {
	run := Build("1", 1, nil)
	run.Truncated = true
	if !strings.Contains(RenderMarkdown(run), "已截断") {
		t.Fatal("a truncated run must say so in its rendering")
	}
}
