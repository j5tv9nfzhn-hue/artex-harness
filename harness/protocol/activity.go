package protocol

import (
	"github.com/Autumn-27/artex/db"
)

// KindOf maps a stored activity kind onto a protocol ItemKind.
//
// Activity kinds in the store are free-form strings written by many call sites
// (capture.go emits text/thinking/tool_use/tool_result/result; the engine emits
// round/intercept_request/…). This is the ONE place that decides how they surface
// in the protocol, so the wire vocabulary stays stable even if the emitter grows
// new kinds.
func KindOf(activityKind string) ItemKind {
	switch activityKind {
	case "text":
		return ItemText
	case "thinking":
		return ItemThinking
	case "tool_use":
		return ItemToolUse
	case "tool_result":
		return ItemToolResult
	case "round":
		return ItemRound
	case "intercept_request", "intercept_decision":
		return ItemIntercept
	case "lifecycle", "pause", "resume", "cancel":
		return ItemLifecycle
	default:
		// "result", "system", anything new: keep it visible as text so no
		// information is lost, and let the client branch on Metadata if it cares.
		return ItemText
	}
}

// FromActivity converts one stored activity row into an Envelope.
//
// turn is the Turn the row belongs to. For rows that predate the revision
// feature (or rows written outside a turn) callers pass Turn{} — Rev 0 means
// "before revisions existed", which the replay path treats as a legacy prefix
// rather than an error.
func FromActivity(taskID string, expID int64, turn Turn, seq int, a db.Activity) Envelope {
	item := Item{
		Kind:       KindOf(a.Kind),
		ActivityID: a.ID,
		Worker:     a.Worker,
		Tool:       a.Tool,
		ToolUseID:  a.ToolUseID,
		IsError:    a.IsError,
		Summary:    a.Summary,
		Detail:     a.Detail,
		Metadata:   a.Metadata,
		NodeID:     a.NodeID,
		At:         a.CreatedAt,
	}
	if a.InputTokens != nil || a.OutputTokens != nil ||
		a.CacheReadTokens != nil || a.CacheWriteTokens != nil {
		u := Usage{}
		if a.InputTokens != nil {
			u.InputTokens = *a.InputTokens
		}
		if a.OutputTokens != nil {
			u.OutputTokens = *a.OutputTokens
		}
		if a.CacheReadTokens != nil {
			u.CacheReadTokens = *a.CacheReadTokens
		}
		if a.CacheWriteTokens != nil {
			u.CacheWriteTokens = *a.CacheWriteTokens
		}
		item.Usage = &u
	}
	return Envelope{
		Rev:    turn.Revision,
		Thread: Thread{TaskID: taskID, ExplorationID: expID},
		Turn:   turn,
		Item:   item,
		Seq:    seq,
	}
}

// TurnFromRevision converts a stored turn boundary into a protocol Turn.
func TurnFromRevision(rev int64, kind, ref, summary string) Turn {
	r := HarnessRevisionView{Revision: rev, Kind: kind, Ref: ref, Summary: summary}
	return Turn{Revision: r.Revision, Kind: r.Kind, Ref: r.Ref, Summary: r.Summary}
}

// HarnessRevisionView is a tiny projection so TurnFromRevision does not need to
// import time-bearing structs at call sites.
type HarnessRevisionView struct {
	Revision int64
	Kind     string
	Ref      string
	Summary  string
}
