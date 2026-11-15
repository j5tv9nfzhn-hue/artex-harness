package replay

import (
	"context"
	"errors"
	"fmt"

	"github.com/Autumn-27/artex/db"
)

// ErrNoTask reports an unknown / deleted task.
var ErrNoTask = errors.New("task not found")

// Load rebuilds a run slice from PostgreSQL.
//
// from/to are inclusive revision bounds; pass from=0 to include the legacy prefix
// (items recorded before revisions existed).
//
// The loader caps how many items it pulls; a capped run is marked Truncated and
// the consistency gate refuses to judge on it (a partial replay cannot prove a
// behaviour match).
func Load(ctx context.Context, d *db.DB, taskID int64, from, to int64) (*Run, error) {
	if d == nil || taskID <= 0 {
		return nil, ErrNoTask
	}
	t, err := d.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	if t == nil {
		return nil, ErrNoTask
	}

	const maxItems = 5000
	links, err := d.TurnItemsInRange(taskID, from, to, maxItems)
	if err != nil {
		return nil, fmt.Errorf("turn items: %w", err)
	}
	truncated := len(links) == maxItems

	ids := make([]int64, 0, len(links))
	revOf := make(map[int64]int64, len(links))
	for _, l := range links {
		ids = append(ids, l.ActivityID)
		revOf[l.ActivityID] = l.Revision
	}

	events := make([]Event, 0, len(ids))
	if len(ids) > 0 {
		store := d.Exploration(t.ExplorationID)
		acts, err := store.ActivityByIDsWithUsage(ids)
		if err != nil {
			return nil, fmt.Errorf("load activities: %w", err)
		}
		for _, a := range acts {
			e := Event{
				Revision:   revOf[a.ID],
				ActivityID: a.ID,
				Kind:       a.Kind,
				Worker:     a.Worker,
				Tool:       a.Tool,
				ToolUseID:  a.ToolUseID,
				Summary:    a.Summary,
				Detail:     a.Detail,
				IsError:    a.IsError,
				NodeID:     a.NodeID,
				At:         a.CreatedAt,
			}
			if a.InputTokens != nil {
				e.InputTokens = *a.InputTokens
			}
			if a.OutputTokens != nil {
				e.OutputTokens = *a.OutputTokens
			}
			if a.CacheReadTokens != nil {
				e.CacheReadTokens = *a.CacheReadTokens
			}
			if a.CacheWriteTokens != nil {
				e.CacheWriteTokens = *a.CacheWriteTokens
			}
			events = append(events, e)
		}
	}

	// Prefix completeness: some rows are written straight to the store without
	// passing the engine's single emit point (goal decomposition at task creation
	// is one), so they carry no turn link. They are the run's head — replaying
	// "from the beginning" must include them, marked as revision 0 (the protocol's
	// legacy-prefix convention). Bounded to one page so a huge task cannot make a
	// replay expensive; the cap is well above any real prefix.
	if len(ids) > 0 && ids[0] > 1 {
		store := d.Exploration(t.ExplorationID)
		const prefixCap = 500
		head, _, err := store.ActivityList(nil, 0, prefixCap)
		if err == nil {
			linked := make(map[int64]bool, len(ids))
			for _, id := range ids {
				linked[id] = true
			}
			for _, a := range head {
				if a.ID >= ids[0] {
					break // reached the linked window; the rest is handled above
				}
				if linked[a.ID] {
					continue
				}
				events = append(events, Event{
					Revision:   0,
					ActivityID: a.ID,
					Kind:       a.Kind,
					Worker:     a.Worker,
					Tool:       a.Tool,
					ToolUseID:  a.ToolUseID,
					Summary:    a.Summary,
					Detail:     a.Detail,
					IsError:    a.IsError,
					NodeID:     a.NodeID,
					At:         a.CreatedAt,
				})
			}
			if len(head) == prefixCap {
				truncated = true
			}
		}
	}

	run := Build(fmt.Sprint(t.ID), t.ExplorationID, events)
	run.From, run.To = from, to
	run.Truncated = truncated

	// Attach persisted turn boundaries (kind/ref/summary). Rows are read in
	// revision order; the store returns them ascending already.
	revs, err := d.ListRevisions(taskID, from-1, 1000)
	if err != nil {
		return nil, fmt.Errorf("revisions: %w", err)
	}
	for _, rv := range revs {
		if to > 0 && rv.Revision > to {
			continue
		}
		run.SetTurnMeta(rv.Revision, rv.TurnKind, rv.TurnRef, rv.Summary, rv.CreatedAt)
	}
	return run, nil
}

// LoadAll is Load over the whole recorded history of a task.
func LoadAll(ctx context.Context, d *db.DB, taskID int64) (*Run, error) {
	return Load(ctx, d, taskID, 0, 0)
}
