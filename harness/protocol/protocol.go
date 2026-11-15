// Package protocol defines the harness wire protocol: Thread / Turn / Item plus
// the event Envelope that carries them over SSE.
//
// Why a bespoke protocol instead of reusing the existing activity DTO: the same
// reason OpenAI's Codex app-server needed one — approval flows, streaming diffs
// and thread persistence are not "tool calls". An activity row is a flat log
// line; a client that must (a) render an interactive session, (b) resume after a
// disconnect at an exact position, and (c) replay a run deterministically needs a
// three-level identity that a flat row cannot express:
//
//	Thread  = one task's run          (task id + exploration id)
//	Turn    = one round inside it     (revision number + kind + ref)
//	Item    = one unit inside a turn  (tool_use / tool_result / text / round / …)
//
// Revision is the ordering key for all three: it is monotonic per task, persisted
// in harness_revisions, and the single value a client needs to say "I have up to
// here, send me the rest".
package protocol

import (
	"encoding/json"
	"time"
)

// ItemKind is the discriminator for one unit of work inside a Turn.
//
// The set is deliberately a superset-friendly string (not an enum with a CHECK
// constraint) so new kinds can be emitted without a migration; clients must
// ignore kinds they do not understand.
type ItemKind string

const (
	// ItemText is an assistant text fragment, already coalesced by the emitter.
	ItemText ItemKind = "text"
	// ItemThinking is a reasoning/thinking fragment.
	ItemThinking ItemKind = "thinking"
	// ItemToolUse is a tool about to run.
	ItemToolUse ItemKind = "tool_use"
	// ItemToolResult is the corresponding result.
	ItemToolResult ItemKind = "tool_result"
	// ItemRound is a Turn boundary marker ("第 N 轮规划").
	ItemRound ItemKind = "round"
	// ItemIntercept is an approval request / decision.
	ItemIntercept ItemKind = "intercept"
	// ItemLifecycle is a task-level lifecycle note (paused/resumed/settled).
	ItemLifecycle ItemKind = "lifecycle"
	// ItemContext is a harness-injected context or kernel-decision row.
	ItemContext ItemKind = "context"
)

// Item is one unit of work inside a Turn.
type Item struct {
	Kind ItemKind `json:"kind"`
	// ActivityID is the backing activity row id (0 for synthetic items). It is the
	// SSE resume cursor: SSE `id:` is set from it so a browser reconnect resumes
	// exactly where it dropped, the same contract the legacy activity stream has.
	ActivityID int64           `json:"activity_id,omitempty"`
	Worker     string          `json:"worker,omitempty"` // planner | work#2 | system
	Tool       string          `json:"tool,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Detail     string          `json:"detail,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	// NodeID is the exploration node the item is anchored to (intent/fact/…).
	NodeID *int64 `json:"node_id,omitempty"`
	// Usage is present on terminal result items only.
	Usage *Usage `json:"usage,omitempty"`
	// At is the item's own timestamp (activity rows carry one; live items may not).
	At time.Time `json:"at,omitempty"`
}

// Usage mirrors the token accounting carried on terminal activity rows.
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

// Thread identifies one task's run. It is stable for the whole task lifetime.
type Thread struct {
	TaskID        string `json:"task_id"`
	ExplorationID int64  `json:"exploration_id"`
}

// Turn identifies one round inside a Thread.
//
// Ref is human-oriented (planner#7, work#12) and is NOT unique — the same intent
// can produce several turns (initial run, retry, human message). Revision is the
// unique key.
type Turn struct {
	Revision int64     `json:"revision"`
	Kind     string    `json:"kind"` // planner | work | system
	Ref      string    `json:"ref,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	At       time.Time `json:"at,omitempty"`
}

// Envelope is what goes over the wire: one Item, addressed inside Thread/Turn,
// with the revision it belongs to.
type Envelope struct {
	// Rev is the Turn revision this item belongs to. Monotonic per task.
	Rev    int64  `json:"rev"`
	Thread Thread `json:"thread"`
	Turn   Turn   `json:"turn"`
	Item   Item   `json:"item"`
	// Seq is the item's position inside the turn, assigned by the emitter
	// (1-based). Lets a client order items that share a revision.
	Seq int `json:"seq,omitempty"`
}

// RevisionsResponse is the catch-up payload for a client resuming at `since`.
type RevisionsResponse struct {
	Task      string      `json:"task"`
	Since     int64       `json:"since"`
	Revisions []Turn      `json:"revisions"`
	Latest    int64       `json:"latest"`
	HasMore   bool        `json:"has_more"`
	Items     []Envelope  `json:"items,omitempty"`
	Meta      interface{} `json:"meta,omitempty"`
}
