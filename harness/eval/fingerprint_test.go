package eval

import (
	"testing"
	"time"

	"github.com/Autumn-27/artex/harness/replay"
)

func mkRun(rev int64, kind string, items ...replay.Event) *replay.Run {
	for i := range items {
		items[i].Revision = rev
		if items[i].At.IsZero() {
			items[i].At = time.Unix(int64(i+1), 0)
		}
		if items[i].ActivityID == 0 {
			items[i].ActivityID = rev*100 + int64(i+1)
		}
	}
	r := replay.Build("1", 1, items)
	// Build leaves the turn kind empty (boundaries come from another table in the
	// real loader); stamp it here so the fingerprint sees a realistic run.
	r.SetTurnMeta(rev, kind, "", "", time.Time{})
	return r
}

func blockEvent(tool, msg string) replay.Event {
	return replay.Event{Kind: "tool_result", Tool: tool, IsError: true, Summary: msg}
}

func TestComputeStepsToolsAndTurns(t *testing.T) {
	run := mkRun(1, "planner",
		replay.Event{Kind: "tool_use", Tool: "Bash", Summary: "ls"},
		blockEvent("Bash", GuardBlockMarker+"此调用被平台拦截。原因：x。此操作被禁止。"),
	)
	fp := Compute(run)
	if fp.Turns != 1 || fp.Items != 2 {
		t.Fatalf("turns/items = %d/%d", fp.Turns, fp.Items)
	}
	if fp.Tools["Bash"] != 1 {
		t.Fatalf("tool multiset = %v", fp.Tools)
	}
	if fp.TurnKinds["planner"] != 1 {
		t.Fatalf("turn kinds = %v", fp.TurnKinds)
	}
	if len(fp.Steps) != 3 { // turn:planner + 2 items
		t.Fatalf("steps = %v", fp.Steps)
	}
	if fp.Hash == "" {
		t.Fatal("empty fingerprint hash")
	}
}

// TestBlocksClassifiesByMarker is the load-bearing distinction: a platform block
// must not be mistaken for a target-side error (an agent that does will try to
// evade a policy decision instead of pivoting).
func TestBlocksClassifiesByMarker(t *testing.T) {
	run := mkRun(1, "work",
		blockEvent("Bash", GuardBlockMarker+"此调用被平台拦截。原因：禁止执行此工具。此操作被禁止。"),
		blockEvent("Write", "Blocked by hook: 策略拒绝"),
		blockEvent("Bash", "curl: (7) Failed to connect"), // ordinary tool error
	)
	blocks := Blocks(run)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 (the plain tool error is not a block): %+v", len(blocks), blocks)
	}
	if blocks[0].Kind != "rule" || blocks[1].Kind != "hook" {
		t.Fatalf("kinds = %q,%q", blocks[0].Kind, blocks[1].Kind)
	}
	for _, b := range blocks {
		if b.Tool == "" {
			t.Fatalf("block lost its tool: %+v", b)
		}
	}
}

func TestRefusalTextsDedupedAndSorted(t *testing.T) {
	run := mkRun(1, "work",
		blockEvent("Bash", GuardBlockMarker+"B"),
		blockEvent("Bash", GuardBlockMarker+"B"),
		blockEvent("Bash", GuardBlockMarker+"A"),
	)
	texts := RefusalTexts(Blocks(run))
	if len(texts) != 2 {
		t.Fatalf("texts = %v", texts)
	}
	if texts[0] >= texts[1] {
		t.Fatalf("texts not sorted: %v", texts)
	}
}

func TestBlockedCallsKeyedByKindAndTool(t *testing.T) {
	run := mkRun(1, "work",
		blockEvent("Bash", GuardBlockMarker+"x"),
		blockEvent("Bash", GuardBlockMarker+"y"),
		blockEvent("Write", "Blocked by hook: z"),
	)
	calls := BlockedCalls(Blocks(run))
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
	if calls[0] != "hook:Write" || calls[1] != "rule:Bash" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestTerminalReasonsBuckets(t *testing.T) {
	run := replay.Build("1", 1, []replay.Event{
		{Revision: 1, ActivityID: 1, Kind: "result"},
		{Revision: 2, ActivityID: 2, Kind: "tool_result", IsError: true},
		{Revision: 3, ActivityID: 3, Kind: "text"},
	})
	got := TerminalReasons(run)
	if got["result"] != 1 || got["error"] != 1 || got["text"] != 1 {
		t.Fatalf("terminal reasons = %v", got)
	}
}

func TestComputeNilRunSafe(t *testing.T) {
	fp := Compute(nil)
	if fp.Turns != 0 || fp.Hash == "" {
		t.Fatalf("nil run must produce an empty but well-formed fingerprint: %+v", fp)
	}
	if Blocks(nil) != nil {
		t.Fatal("nil run must have no blocks")
	}
}
