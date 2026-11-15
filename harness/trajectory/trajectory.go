// Package trajectory turns a replay.Run into the rows a "trace view" renders.
//
// A chat transcript answers "what was said". A trajectory answers "what happened,
// in what order, at what cost" — one row per system / user / context / assistant /
// tool event, grouped by revision, with per-turn token accounting and elapsed
// time. That is the observable surface a long-running agent needs for post-mortem.
//
// The transformation is pure (no DB, no clock injection) so the frontend contract
// can be pinned by tests: the loader already guarantees the ordering of events.
package trajectory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/harness/replay"
)

// Kind is the row's role in the transcript.
type Kind string

const (
	KindSystem    Kind = "system"
	KindUser      Kind = "user"
	KindContext   Kind = "context"
	KindAssistant Kind = "assistant"
	KindTool      Kind = "tool"
)

// Row is one line of the trajectory table.
type Row struct {
	ID           string    `json:"id"`
	Revision     int64     `json:"revision"`
	Kind         Kind      `json:"kind"`
	Label        string    `json:"label"`            // tool name, "系统提示词", "用户", …
	Preview      string    `json:"preview"`          // short one-line preview
	Detail       string    `json:"detail,omitempty"` // full body (lazy in the UI)
	Tool         string    `json:"tool,omitempty"`   // tool name for tool rows
	ToolUseID    string    `json:"tool_use_id,omitempty"`
	IsError      bool      `json:"is_error,omitempty"`
	At           time.Time `json:"at,omitempty"`
	InputTokens  int       `json:"input_tokens,omitempty"`
	OutputTokens int       `json:"output_tokens,omitempty"`
	// Subagent is set when the row belongs to a delegated agent's own trace.
	// The UI uses it to offer "查看子代理轨迹" instead of silently flattening it.
	Subagent string `json:"subagent,omitempty"`
}

// Turn groups rows by revision and carries the turn's aggregate accounting.
type Turn struct {
	Revision     int64     `json:"revision"`
	Kind         string    `json:"kind"`
	Ref          string    `json:"ref,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	At           time.Time `json:"at,omitempty"`
	Rows         []Row     `json:"rows"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	DurationMS   int64     `json:"duration_ms"`
	ToolCount    int       `json:"tool_count"`
	HasError     bool      `json:"has_error"`
}

// View is the whole trajectory for one task (or one revision window).
type View struct {
	Task          string `json:"task"`
	ExplorationID int64  `json:"exploration_id"`
	From          int64  `json:"from"`
	To            int64  `json:"to"`
	Truncated     bool   `json:"truncated"`
	Turns         []Turn `json:"turns"`
	Minimap       []Lane `json:"minimap"`
	TotalTokens   int    `json:"total_tokens"`
}

// Lane is one swimlane of the minimap (per-turn input tokens scaled to 0..100).
type Lane struct {
	Revision int64 `json:"revision"`
	Input    int   `json:"input"`
	Output   int   `json:"output"`
	Tools    int   `json:"tools"`
	Height   int   `json:"height"` // 0..100, relative to the largest turn
}

const previewLimit = 200

// Build converts a replay.Run into a trajectory View.
//
// Classification is mechanical and deliberately conservative: an unrecognised
// activity kind is still rendered (as context) rather than dropped — losing a
// line is worse than labelling it imprecisely.
func Build(run *replay.Run) View {
	v := View{
		Task: run.Task, ExplorationID: run.ExplorationID,
		From: run.From, To: run.To, Truncated: run.Truncated,
		Turns: make([]Turn, 0, len(run.Turns)),
	}
	var maxTokens int
	var sysHash string
	for _, rt := range run.Turns {
		t := Turn{Revision: rt.Revision, Kind: rt.Kind, Ref: rt.Ref, Summary: rt.Summary, At: rt.At}
		var first, last time.Time
		for _, e := range rt.Items {
			row := rowOf(e, sysHash)
			if e.Kind == "system" || e.Kind == "system_prompt" {
				sysHash = hash(e.Detail)
				row.Detail = e.Detail
			}
			t.Rows = append(t.Rows, row)
			if !first.IsZero() || !e.At.IsZero() {
				if first.IsZero() || e.At.Before(first) {
					first = e.At
				}
				if e.At.After(last) {
					last = e.At
				}
			}
			// usage rows carry CUMULATIVE counters for the model turn. Taking the
			// max (not the sum) avoids double counting when several usage rows
			// land in one revision.
			if e.InputTokens > t.InputTokens {
				t.InputTokens = e.InputTokens
			}
			if e.OutputTokens > t.OutputTokens {
				t.OutputTokens = e.OutputTokens
			}
			if row.Kind == KindTool {
				t.ToolCount++
			}
			if row.IsError {
				t.HasError = true
			}
		}
		if !first.IsZero() && !last.IsZero() {
			t.DurationMS = last.Sub(first).Milliseconds()
		}
		v.TotalTokens += t.InputTokens + t.OutputTokens
		if n := t.InputTokens + t.OutputTokens; n > maxTokens {
			maxTokens = n
		}
		v.Turns = append(v.Turns, t)
	}
	for _, t := range v.Turns {
		h := 8
		if maxTokens > 0 {
			h = 8 + (t.InputTokens+t.OutputTokens)*92/maxTokens
		}
		if h > 100 {
			h = 100
		}
		v.Minimap = append(v.Minimap, Lane{
			Revision: t.Revision, Input: t.InputTokens, Output: t.OutputTokens,
			Tools: t.ToolCount, Height: h,
		})
	}
	return v
}

// rowOf maps one replay.Event to a trajectory Row.
func rowOf(e replay.Event, prevSysHash string) Row {
	r := Row{
		ID:        fmt.Sprintf("%d-%d", e.Revision, e.ActivityID),
		Revision:  e.Revision,
		At:        e.At,
		IsError:   e.IsError,
		Tool:      e.Tool,
		ToolUseID: e.ToolUseID,
		Subagent:  subagentOf(e.Worker),
	}
	body := e.Detail
	if strings.TrimSpace(body) == "" {
		body = e.Summary
	}
	r.Preview = clip(body, previewLimit)
	r.Detail = body
	r.InputTokens = e.InputTokens
	r.OutputTokens = e.OutputTokens
	switch e.Kind {
	case "system", "system_prompt":
		r.Kind, r.Label = KindSystem, "系统提示词"
		if prevSysHash != "" && hash(e.Detail) != prevSysHash {
			r.Label = "系统提示词已更新"
		}
	case "user", "human", "say":
		r.Kind, r.Label = KindUser, "用户"
	case "usage":
		r.Kind, r.Label = KindContext, "用量"
		r.Preview = fmt.Sprintf("input %d · output %d · cache %d/%d",
			e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens)
	case "context", "skill", "memory", "compact", "reminder", "hint", "attachment",
		"permission_decision", "recovery":
		r.Kind, r.Label = KindContext, "上下文"
		if e.Kind == "permission_decision" {
			r.Label = "权限策略"
		} else if e.Kind == "recovery" {
			r.Label = "恢复"
		}
	case "thinking":
		r.Kind, r.Label = KindAssistant, "思考"
	case "tool_use", "tool_result":
		r.Kind = KindTool
		r.Label = e.Tool
		if r.Label == "" {
			r.Label = "工具"
		}
		if e.Kind == "tool_result" {
			r.Label += " → 结果"
		}
	case "result":
		r.Kind, r.Label = KindAssistant, "助手"
	case "text":
		r.Kind, r.Label = KindAssistant, "助手"
	case "round":
		r.Kind, r.Label = KindContext, "轮次"
	case "intercept_request", "intercept_decision":
		r.Kind, r.Label = KindContext, "审批"
	case "lifecycle":
		r.Kind, r.Label = KindContext, "生命周期"
	default:
		r.Kind, r.Label = KindContext, e.Kind
		if r.Label == "" {
			r.Label = "上下文"
		}
	}
	return r
}

// SystemPromptVersions returns the distinct system-prompt bodies in revision
// order, so the detail panel can show "what changed between versions" without
// persisting a separate history table.
func SystemPromptVersions(run *replay.Run) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range run.Turns {
		for _, e := range t.Items {
			if e.Kind != "system" && e.Kind != "system_prompt" {
				continue
			}
			h := hash(e.Detail)
			if seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, e.Detail)
		}
	}
	return out
}

// Filter reduces a view to one subagent's rows. An empty agent returns the view
// unchanged; an unknown agent returns an empty view (the UI must say "no trace").
func Filter(v View, agent string) View {
	if strings.TrimSpace(agent) == "" {
		return v
	}
	out := View{Task: v.Task, ExplorationID: v.ExplorationID, From: v.From, To: v.To, Truncated: v.Truncated}
	for _, t := range v.Turns {
		nt := Turn{Revision: t.Revision, Kind: t.Kind, Ref: t.Ref, Summary: t.Summary, At: t.At}
		for _, r := range t.Rows {
			if r.Subagent == agent {
				nt.Rows = append(nt.Rows, r)
			}
		}
		if len(nt.Rows) > 0 {
			out.Turns = append(out.Turns, nt)
		}
	}
	return out
}

// Subagents lists the delegated agents that appear in a view, sorted.
func Subagents(run *replay.Run) []string {
	set := map[string]bool{}
	for _, t := range run.Turns {
		for _, e := range t.Items {
			if a := subagentOf(e.Worker); a != "" {
				set[a] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func subagentOf(worker string) string {
	// Worker labels from the HARNESS runtime: "harness" is the main agent;
	// delegated work is emitted with a worker like "subagent:recon#3".
	if strings.HasPrefix(worker, "subagent:") {
		return strings.TrimSpace(strings.TrimPrefix(worker, "subagent:"))
	}
	return ""
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
