// Intent content editing (harness S2).
//
// Upstream has NO path that changes an intent's content. Once addOneIntent
// creates a node, only its STATE can move (open/running/paused/done/blocked/…);
// summary, asset_ids and priority are frozen. The evaluation (docs/01 §3 ①) found
// this is the single most concrete blocker to human steering: when the planner
// dispatches a wrong direction, the only remedy was "cancel (with a mandatory
// reason) and hope the model re-derives it".
//
// These two functions close that gap at the store layer. They are additive: no
// existing query changes, no existing behaviour changes.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrIntentNotEditable means the node is not an intent of this exploration, or
	// it was soft-deleted (a deleted intent keeps its lineage but is out of play).
	ErrIntentNotEditable = errors.New("intent not editable")
	// ErrIntentEditEmpty means the patch carried no editable field.
	ErrIntentEditEmpty = errors.New("no editable field in patch")
)

// IntentPatch carries the fields a human may override. Nil = leave unchanged.
//
// Pointer fields (not zero values) are deliberate: setting a priority to 0 must be
// distinguishable from "do not touch the priority".
type IntentPatch struct {
	Summary  *string  `json:"summary,omitempty"`
	AssetIDs *[]int64 `json:"asset_ids,omitempty"`
	Priority *int     `json:"priority,omitempty"`
}

// IsEmpty reports whether the patch changes nothing.
func (p IntentPatch) IsEmpty() bool {
	return p.Summary == nil && p.AssetIDs == nil && p.Priority == nil
}

// MaxIntentSummaryRunes bounds the new summary. The upstream planner writes
// summaries well under this; the bound exists so a human (or a hostile caller)
// cannot push an unbounded string into every future prompt.
const MaxIntentSummaryRunes = 2000

// UpdateIntentContent applies a human-authored patch to one intent of this
// exploration and returns the updated node.
//
// Semantics:
//   - summary / asset_ids are merged INTO the existing payload (other keys, e.g.
//     parent_ids or traffic_refs, are preserved) — this is a patch, not a replace;
//   - priority is a real column, so it is updated separately in the same statement;
//   - content_version is bumped, which is what cold-digest uses to decide a folded
//     node's body is stale (agent/coldgraph.go §5.3).
//
// The statement is a single UPDATE guarded on kind='intent' AND state<>'deleted',
// so two concurrent editors serialize on the row and a deleted intent can never be
// resurrected by editing it.
func (s *ExplorationStore) UpdateIntentContent(id int64, patch IntentPatch) (*Node, error) {
	if id <= 0 {
		return nil, fmt.Errorf("%w: bad id", ErrIntentNotEditable)
	}
	if patch.IsEmpty() {
		return nil, ErrIntentEditEmpty
	}
	if patch.Summary != nil {
		summary := strings.TrimSpace(*patch.Summary)
		if summary == "" {
			return nil, fmt.Errorf("%w: summary 不能为空", ErrIntentNotEditable)
		}
		if len([]rune(summary)) > MaxIntentSummaryRunes {
			return nil, fmt.Errorf("%w: summary 超过 %d 字", ErrIntentNotEditable, MaxIntentSummaryRunes)
		}
		patch.Summary = &summary
	}
	if patch.AssetIDs != nil && len(*patch.AssetIDs) > maxIntentAssets {
		return nil, fmt.Errorf("%w: asset_ids 超过 %d 个", ErrIntentNotEditable, maxIntentAssets)
	}

	// Build the payload fragment. Only keys present in the patch are set, so a
	// patch touching summary alone leaves asset_ids/parent_ids intact.
	frag := map[string]any{}
	if patch.Summary != nil {
		frag["summary"] = *patch.Summary
		frag["edited_by"] = "human" // provenance: this direction was course-corrected
	}
	if patch.AssetIDs != nil {
		ids := make([]int64, 0, len(*patch.AssetIDs))
		for _, a := range *patch.AssetIDs {
			if a > 0 {
				ids = append(ids, a)
			}
		}
		frag["asset_ids"] = ids
		// Legacy singular keys are stripped by the `- 'target_ids' - 'target_id'`
		// operator in the UPDATE itself (not re-added as JSON null here), so a
		// reader can no longer fall back to a stale id.
	}
	fragJSON, err := json.Marshal(frag)
	if err != nil {
		return nil, err
	}

	var priority any
	if patch.Priority != nil {
		priority = *patch.Priority
	}

	var updated int64
	err = s.q().QueryRow(`
WITH target AS (
    SELECT id FROM exploration_nodes
    WHERE id=$1 AND exploration_id=$2 AND kind='intent' AND state <> 'deleted'
)
UPDATE exploration_nodes n
SET payload = (n.payload - 'target_ids' - 'target_id') || $3::jsonb,
    priority = COALESCE($4::int, n.priority),
    content_version = n.content_version + 1
FROM target
WHERE n.id = target.id
RETURNING n.id`, id, s.expID, string(fragJSON), priority).Scan(&updated)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIntentNotEditable, err)
	}
	return s.GetNode(id)
}

// maxIntentAssets mirrors the planner's practical ceiling; keeps a patch from
// turning one intent into an unbounded anchor set.
const maxIntentAssets = 256

// SetIntentPriorities rewrites the ordering of the open frontier.
//
// The frontier query is `ORDER BY priority DESC, id ASC` (db/exploration.go
// Frontier), so "put A, then B, then C" is expressed as strictly decreasing
// priorities — no new column, no new index, no change to the claim path.
//
// constraints maps intent id → priority for the intents the caller wants to
// reorder; intents not mentioned keep their priority. All writes happen in one
// transaction so a reader never sees a half-applied order.
func (s *ExplorationStore) SetIntentPriorities(ctx context.Context, constraints map[int64]int) error {
	if len(constraints) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for id, prio := range constraints {
		if id <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE exploration_nodes
SET priority = $1, content_version = content_version + 1
WHERE id=$2 AND exploration_id=$3 AND kind='intent' AND state='open'`,
			prio, id, s.expID); err != nil {
			return fmt.Errorf("set intent %d priority: %w", id, err)
		}
	}
	return tx.Commit()
}

// OrderFrontier assigns descending priorities in the caller's order, starting from
// the highest current priority so a reorder never accidentally demotes the whole
// frontier below an unrelated intent.
//
// Returns the priority it actually used for the first element (useful for tests
// and for echoing back what changed).
func (s *ExplorationStore) OrderFrontier(ctx context.Context, orderedIDs []int64) (int, error) {
	if len(orderedIDs) == 0 {
		return 0, nil
	}
	var maxPrio int
	if err := s.q().QueryRow(`SELECT COALESCE(MAX(priority),0) FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state='open'`, s.expID).Scan(&maxPrio); err != nil {
		return 0, err
	}
	// Leave a gap of len(ordered) so the ordered block strictly dominates anything
	// the planner assigns later at the same numeric priority.
	top := maxPrio + len(orderedIDs) + 1
	prios := make(map[int64]int, len(orderedIDs))
	for i, id := range orderedIDs {
		prios[id] = top - i
	}
	if err := s.SetIntentPriorities(ctx, prios); err != nil {
		return 0, err
	}
	return top, nil
}
