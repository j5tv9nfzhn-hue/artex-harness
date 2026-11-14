package protocol

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestKindOfMapsStoredKindsToWireKinds(t *testing.T) {
	cases := map[string]ItemKind{
		"text":               ItemText,
		"thinking":           ItemThinking,
		"tool_use":           ItemToolUse,
		"tool_result":        ItemToolResult,
		"round":              ItemRound,
		"intercept_request":  ItemIntercept,
		"intercept_decision": ItemIntercept,
		"lifecycle":          ItemLifecycle,
		"pause":              ItemLifecycle,
		// Unknown / future kinds must degrade to text rather than vanish — losing
		// a record from the transcript is worse than labelling it generically.
		"result":    ItemText,
		"brand_new": ItemText,
		"":          ItemText,
	}
	for in, want := range cases {
		if got := KindOf(in); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromActivityCarriesAddressing(t *testing.T) {
	node := int64(42)
	in := db.Activity{
		ID:        9001,
		NodeID:    &node,
		Worker:    "work#2",
		Kind:      "tool_use",
		Tool:      "Bash",
		ToolUseID: "tu_1",
		Summary:   "nmap -sV",
		Detail:    "detail blob",
		Metadata:  json.RawMessage(`{"k":1}`),
		CreatedAt: time.Unix(1700000000, 0),
	}
	turn := Turn{Revision: 7, Kind: "work", Ref: "work#12"}
	env := FromActivity("55", 12, turn, 3, in)

	if env.Rev != 7 {
		t.Errorf("Rev = %d, want 7", env.Rev)
	}
	if env.Thread.TaskID != "55" || env.Thread.ExplorationID != 12 {
		t.Errorf("thread = %+v", env.Thread)
	}
	if env.Turn.Ref != "work#12" || env.Turn.Kind != "work" {
		t.Errorf("turn = %+v", env.Turn)
	}
	if env.Seq != 3 {
		t.Errorf("Seq = %d, want 3", env.Seq)
	}
	it := env.Item
	if it.Kind != ItemToolUse || it.Tool != "Bash" || it.ToolUseID != "tu_1" {
		t.Errorf("item = %+v", it)
	}
	// ActivityID is the SSE resume cursor; losing it breaks Last-Event-ID resume.
	if it.ActivityID != 9001 {
		t.Errorf("ActivityID = %d, want 9001", it.ActivityID)
	}
	if it.NodeID == nil || *it.NodeID != 42 {
		t.Errorf("NodeID = %v", it.NodeID)
	}
	if string(it.Metadata) != `{"k":1}` {
		t.Errorf("metadata = %s", it.Metadata)
	}
}

func TestFromActivityUsageOnlyWhenPresent(t *testing.T) {
	base := db.Activity{ID: 1, Kind: "result"}
	if env := FromActivity("1", 1, Turn{}, 0, base); env.Item.Usage != nil {
		t.Fatalf("usage should be nil when the row carries no token counts")
	}
	in, out := 100, 20
	withUsage := db.Activity{ID: 2, Kind: "result", InputTokens: &in, OutputTokens: &out}
	u := FromActivity("1", 1, Turn{}, 0, withUsage).Item.Usage
	if u == nil || u.InputTokens != 100 || u.OutputTokens != 20 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestLegacyRowsUseRevisionZero(t *testing.T) {
	// A row that predates revisions (or was emitted outside a turn) must come out
	// as rev 0 — the replay path treats 0 as a legacy prefix, not an error.
	env := FromActivity("9", 3, Turn{}, 0, db.Activity{ID: 5, Kind: "text", Summary: "hi"})
	if env.Rev != 0 {
		t.Fatalf("Rev = %d, want 0", env.Rev)
	}
	if env.Turn.Revision != 0 || env.Turn.Kind != "" {
		t.Fatalf("turn = %+v", env.Turn)
	}
}

func TestTurnFromRevision(t *testing.T) {
	turn := TurnFromRevision(4, "planner", "planner#4", "第 4 轮规划")
	if turn.Revision != 4 || turn.Kind != "planner" || turn.Ref != "planner#4" {
		t.Fatalf("turn = %+v", turn)
	}
}

func TestEnvelopeJSONShape(t *testing.T) {
	env := FromActivity("55", 12, Turn{Revision: 7, Kind: "planner"}, 1,
		db.Activity{ID: 3, Kind: "round", Worker: "planner", Summary: "第 7 轮规划"})
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"rev", "thread", "turn", "item", "seq"} {
		if _, ok := back[key]; !ok {
			t.Errorf("envelope JSON missing %q: %s", key, b)
		}
	}
}
