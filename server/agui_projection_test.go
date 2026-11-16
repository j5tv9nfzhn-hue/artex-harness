package server

// Sprint 4: the console renders an inline approval card from the AG-UI event
// stream. That projection is a pure function, so it is pinned here without a
// model or a database — a mapping regression would make a blocked run invisible
// again, which is exactly the defect the inline card was added to fix.

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestAguiProjectsInterceptRequest(t *testing.T) {
	// Metadata is the copy the list query actually returns (detail is omitted for
	// size), so the projection must prefer it — a card built from detail alone had
	// no pending id and no tool name.
	payload := `{"pending_id":42,"tool":"Write","input":{"file_path":"x.txt","content":"hi"}}`
	events := aguiFromActivity(db.Activity{
		ID: 7, Kind: "intercept_request", Summary: "工具 Write 请求审批 (#42)",
		Metadata: []byte(payload),
	}, "task-1", "run-1", 3)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Type != "INTERCEPT_REQUEST" {
		t.Fatalf("type = %q, want INTERCEPT_REQUEST", ev.Type)
	}
	if ev.Seq != 7 {
		t.Fatalf("seq = %d, want the activity id (resume cursor)", ev.Seq)
	}
	var got struct {
		PendingID int64          `json:"pending_id"`
		Tool      string         `json:"tool"`
		Input     map[string]any `json:"input"`
	}
	if err := json.Unmarshal([]byte(ev.Content), &got); err != nil {
		t.Fatalf("content is not the JSON payload the card parses: %v (%q)", err, ev.Content)
	}
	if got.PendingID != 42 || got.Tool != "Write" {
		t.Fatalf("payload lost its addressing: %+v", got)
	}
	if got.Input["file_path"] != "x.txt" {
		t.Fatalf("input not preserved: %+v", got.Input)
	}
}

func TestAguiProjectsPermissionDecision(t *testing.T) {
	events := aguiFromActivity(db.Activity{
		ID: 8, Kind: "permission_decision", Summary: "权限策略：Write → ask（rule）", Detail: "模式 default",
	}, "task-1", "run-1", 3)
	if len(events) != 1 || events[0].Type != "PERMISSION_DECISION" {
		t.Fatalf("permission_decision not projected: %+v", events)
	}
	if events[0].Seq != 8 {
		t.Fatalf("seq = %d, want 8", events[0].Seq)
	}
}

// An approval request must never be silently dropped: an unmapped kind yields no
// events, so this guards against someone "cleaning up" the switch later.
func TestAguiUnknownKindYieldsNothing(t *testing.T) {
	if evs := aguiFromActivity(db.Activity{ID: 9, Kind: "definitely-not-a-kind"}, "t", "r", 1); len(evs) != 0 {
		t.Fatalf("unknown kind produced %d events, want 0", len(evs))
	}
}
