// Package replay rebuilds a finished (or in-progress) run from its recorded
// history, so it can be inspected, diffed and gated on.
//
// Why this exists: upstream could only "read the logs". Its activity table is a
// flat append log with no notion of a round, and its LLM/HTTP traces live in three
// unrelated places (activity, llmrec, traffic). The harness protocol (S1) adds the
// missing structure — Turn/Item with a revision — so a run becomes a value that
// can be replayed, compared between two builds, and asserted on in CI.
//
// This package holds the pure logic (grouping, ordering, rendering). Loading from
// PostgreSQL lives in loader.go so the core is testable with no database.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Event is one recorded item, flattened for replay.
type Event struct {
	Revision   int64     `json:"revision"`
	ActivityID int64     `json:"activity_id"`
	Kind       string    `json:"kind"`
	Worker     string    `json:"worker,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	ToolUseID  string    `json:"tool_use_id,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	IsError    bool      `json:"is_error,omitempty"`
	NodeID     *int64    `json:"node_id,omitempty"`
	At         time.Time `json:"at"`
	// Token accounting on usage/result rows. Cumulative per model turn (the
	// emitter writes l.usage), so the trajectory shows the latest value rather
	// than summing every row.
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Turn groups the items emitted during one round.
type Turn struct {
	Revision int64     `json:"revision"`
	Kind     string    `json:"kind"` // planner | work | system | "" (legacy)
	Ref      string    `json:"ref,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	At       time.Time `json:"at,omitempty"`
	Items    []Event   `json:"items"`
}

// Run is one task's replayable history slice.
type Run struct {
	Task          string `json:"task"`
	ExplorationID int64  `json:"exploration_id"`
	From          int64  `json:"from"`
	To            int64  `json:"to"`
	Turns         []Turn `json:"turns"`
	// Truncated reports that the loader hit its row cap; a truncated run must not
	// be used for a pass/fail verdict (the gate refuses it).
	Truncated bool `json:"truncated,omitempty"`
}

// Build groups events into turns, ordered by revision then activity id.
//
// Duplicate activity ids are dropped (the same row can be linked twice if a
// re-emit happened), which keeps fingerprinting stable.
func Build(task string, expID int64, events []Event) *Run {
	seen := make(map[int64]bool, len(events))
	byRev := map[int64]*Turn{}
	order := make([]int64, 0, 8)

	// Legacy rows (revision 0) are kept in a synthetic turn so a run that predates
	// revisions still replays instead of losing its head.
	const legacyRev = 0

	for _, ev := range events {
		if ev.ActivityID != 0 {
			if seen[ev.ActivityID] {
				continue
			}
			seen[ev.ActivityID] = true
		}
		rev := ev.Revision
		t, ok := byRev[rev]
		if !ok {
			t = &Turn{Revision: rev}
			byRev[rev] = t
			order = append(order, rev)
		}
		t.Items = append(t.Items, ev)
		if t.At.IsZero() && !ev.At.IsZero() {
			t.At = ev.At
		}
	}
	_ = legacyRev

	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	run := &Run{Task: task, ExplorationID: expID, Turns: make([]Turn, 0, len(order))}
	for _, rev := range order {
		t := byRev[rev]
		sort.Slice(t.Items, func(i, j int) bool {
			if t.Items[i].ActivityID != t.Items[j].ActivityID {
				return t.Items[i].ActivityID < t.Items[j].ActivityID
			}
			return t.Items[i].At.Before(t.Items[j].At)
		})
		run.Turns = append(run.Turns, *t)
	}
	return run
}

// SetTurnMeta attaches the persisted turn boundary (kind/ref/summary) to the
// matching turn. Called after Build because boundaries and items arrive from
// different tables.
func (r *Run) SetTurnMeta(rev int64, kind, ref, summary string, at time.Time) {
	for i := range r.Turns {
		if r.Turns[i].Revision == rev {
			r.Turns[i].Kind, r.Turns[i].Ref, r.Turns[i].Summary = kind, ref, summary
			if !at.IsZero() {
				r.Turns[i].At = at
			}
			return
		}
	}
	// A boundary with no items still matters (a planner round that dispatched
	// nothing is a real, observable event).
	r.Turns = append(r.Turns, Turn{Revision: rev, Kind: kind, Ref: ref, Summary: summary, At: at})
	sort.Slice(r.Turns, func(i, j int) bool { return r.Turns[i].Revision < r.Turns[j].Revision })
}

// ItemCount is the number of replayed items.
func (r *Run) ItemCount() int {
	n := 0
	for _, t := range r.Turns {
		n += len(t.Items)
	}
	return n
}

// RenderMarkdown produces a deterministic textual rendering. Determinism matters:
// this output is what a human diffs between two builds, so it must not depend on
// map iteration order or wall-clock.
func RenderMarkdown(r *Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# replay · task %s (exploration %d)\n", r.Task, r.ExplorationID)
	fmt.Fprintf(&b, "\nrevisions %d..%d · %d turns · %d items\n", r.From, r.To, len(r.Turns), r.ItemCount())
	if r.Truncated {
		b.WriteString("\n> ⚠ 已截断：本次回放未覆盖完整历史，不可用于一致性判定\n")
	}
	for _, t := range r.Turns {
		fmt.Fprintf(&b, "\n## rev %d · %s", t.Revision, orDash(t.Kind))
		if t.Ref != "" {
			fmt.Fprintf(&b, " · %s", t.Ref)
		}
		if t.Summary != "" {
			fmt.Fprintf(&b, " — %s", t.Summary)
		}
		b.WriteString("\n")
		for _, it := range t.Items {
			line := itemLine(it)
			fmt.Fprintf(&b, "- [%s] %s\n", orDash(it.Kind), line)
		}
		if len(t.Items) == 0 {
			b.WriteString("- (no items)\n")
		}
	}
	return b.String()
}

func itemLine(it Event) string {
	var parts []string
	if it.Tool != "" {
		parts = append(parts, it.Tool)
	}
	if it.Summary != "" {
		parts = append(parts, oneLine(it.Summary))
	}
	if it.IsError {
		parts = append(parts, "(error)")
	}
	if len(parts) == 0 {
		return oneLine(it.Detail)
	}
	return strings.Join(parts, " · ")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) > 240 {
		// Truncation must be byte-stable across runs, so cut on a rune boundary.
		cut := 240
		for cut > 0 && !utf8Start(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// Hash is a stable digest of the rendered run — usable as a replay cache key and
// as the "did anything change at all" signal.
func (r *Run) Hash() string {
	h := sha256.Sum256([]byte(RenderMarkdown(r)))
	return hex.EncodeToString(h[:])
}
