package trajectory

import (
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/harness/replay"
)

func run() *replay.Run {
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return &replay.Run{Task: "42", ExplorationID: 7, From: 0, To: 3,
		Turns: []replay.Turn{
			{Revision: 1, Kind: "harness", Ref: "run", At: t0, Items: []replay.Event{
				{Revision: 1, ActivityID: 1, Kind: "system", Detail: "SYSTEM v1", At: t0},
				{Revision: 1, ActivityID: 2, Kind: "user", Detail: "开始侦察", At: t0.Add(time.Second)},
				{Revision: 1, ActivityID: 3, Kind: "tool_use", Tool: "Bash", ToolUseID: "c1", Detail: `{"command":"id"}`, At: t0.Add(2 * time.Second)},
				{Revision: 1, ActivityID: 4, Kind: "tool_result", Tool: "Bash", ToolUseID: "c1", Detail: "uid=0", At: t0.Add(3 * time.Second)},
				{Revision: 1, ActivityID: 5, Kind: "result", Detail: "完成", At: t0.Add(4 * time.Second)},
				{Revision: 1, ActivityID: 6, Kind: "usage", Detail: "", At: t0.Add(4 * time.Second), InputTokens: 1200, OutputTokens: 300},
				{Revision: 1, ActivityID: 7, Kind: "usage", Detail: "", At: t0.Add(5 * time.Second), InputTokens: 1200, OutputTokens: 300},
			}},
			{Revision: 2, Kind: "harness", Ref: "say", At: t0.Add(time.Minute), Items: []replay.Event{
				{Revision: 2, ActivityID: 6, Kind: "system", Detail: "SYSTEM v2", At: t0.Add(time.Minute)},
				{Revision: 2, ActivityID: 7, Kind: "text", Detail: "继续", At: t0.Add(time.Minute + time.Second)},
				{Revision: 2, ActivityID: 8, Kind: "tool_use", Tool: "Agent", Worker: "subagent:recon#3", Detail: "派发", At: t0.Add(time.Minute + 2*time.Second)},
			}},
		},
	}
}

func TestBuildGroupsRowsAndComputesTiming(t *testing.T) {
	v := Build(run())
	if len(v.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(v.Turns))
	}
	first := v.Turns[0]
	if len(first.Rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(first.Rows))
	}
	if first.Rows[0].Kind != KindSystem || first.Rows[1].Kind != KindUser || first.Rows[2].Kind != KindTool || first.Rows[4].Kind != KindAssistant {
		t.Fatalf("row kinds = %v", []Kind{first.Rows[0].Kind, first.Rows[1].Kind, first.Rows[2].Kind, first.Rows[4].Kind})
	}
	if first.DurationMS != 5000 {
		t.Fatalf("duration = %dms, want 5000", first.DurationMS)
	}
	// Two identical cumulative usage rows must NOT be summed.
	if first.InputTokens != 1200 || first.OutputTokens != 300 {
		t.Fatalf("turn tokens = %d/%d, want max 1200/300 (cumulative, not summed)", first.InputTokens, first.OutputTokens)
	}
	if first.ToolCount != 2 {
		t.Fatalf("tool count = %d, want 2 (tool_use + tool_result)", first.ToolCount)
	}
	if first.Rows[2].ToolUseID != "c1" || first.Rows[3].ToolUseID != "c1" {
		t.Fatal("tool_use/result must keep the same ToolUseID for pairing")
	}
	if v.Turns[1].Rows[0].Label != "系统提示词已更新" {
		t.Fatalf("a changed system prompt must be labelled as updated, got %q", v.Turns[1].Rows[0].Label)
	}
}

func TestBuildNeverDropsAnEvent(t *testing.T) {
	r := run()
	r.Turns[0].Items = append(r.Turns[0].Items, replay.Event{Revision: 1, ActivityID: 99, Kind: "brand-new-kind", Detail: "keep me"})
	v := Build(r)
	if len(v.Turns[0].Rows) != 8 || !strings.Contains(v.Turns[0].Rows[7].Detail, "keep me") {
		t.Fatal("an unknown activity kind must still be rendered, not dropped")
	}
}

func TestMinimapAndTotals(t *testing.T) {
	v := Build(run())
	if len(v.Minimap) != 2 {
		t.Fatalf("minimap lanes = %d, want 2", len(v.Minimap))
	}
	for _, l := range v.Minimap {
		if l.Height < 8 || l.Height > 100 {
			t.Fatalf("lane height out of range: %d", l.Height)
		}
	}
	if v.TotalTokens != 1500 {
		t.Fatalf("total tokens = %d, want 1500", v.TotalTokens)
	}
}

func TestSystemPromptVersions(t *testing.T) {
	got := SystemPromptVersions(run())
	if len(got) != 2 || got[0] != "SYSTEM v1" || got[1] != "SYSTEM v2" {
		t.Fatalf("versions = %v", got)
	}
}

func TestSubagentsAndFilter(t *testing.T) {
	r := run()
	agents := Subagents(r)
	if len(agents) != 1 || agents[0] != "recon#3" {
		t.Fatalf("subagents = %v", agents)
	}
	v := Build(r)
	f := Filter(v, "recon#3")
	if len(f.Turns) != 1 || len(f.Turns[0].Rows) != 1 {
		t.Fatalf("filtered turns/rows = %d/%d, want 1/1", len(f.Turns), len(f.Turns[0].Rows))
	}
	if len(Filter(v, "unknown").Turns) != 0 {
		t.Fatal("an unknown subagent must produce an empty view, not the whole trace")
	}
	if len(Filter(v, "").Turns) != 2 {
		t.Fatal("an empty filter must return the full view")
	}
}

func TestPreviewIsSingleLineAndBounded(t *testing.T) {
	v := Build(run())
	p := v.Turns[0].Rows[2].Preview
	if strings.Contains(p, "\n") || len(p) > previewLimit+3 {
		t.Fatalf("preview not bounded/single-line: %q", p)
	}
}
