// harness_revisions：Turn（轮次）的持久序号（S1）。
//
// 一次任务的运行被切成若干 Turn（规划轮 / work 轮）。每个 Turn 结束时打一条
// revision，于是：
//   - 事件流有了全局单调的排序键（前端 seq-merge / 断线自愈）；
//   - Replay 可以按 (from, to] 精确取一段；
//   - Eval 可以对轨迹做指纹比对（见 harness/eval）。
//
// revision 同时冗余在 tasks.revision 上（同一事务内自增），使"任务当前版本"
// 无需聚合即可读——前端用它判断自己看到的态势是否 stale。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// HarnessRevision is one recorded Turn boundary.
type HarnessRevision struct {
	ID            int64     `json:"id"`
	TaskID        int64     `json:"task_id"`
	ExplorationID int64     `json:"exploration_id"`
	Revision      int64     `json:"revision"`
	TurnKind      string    `json:"turn_kind"`
	TurnRef       string    `json:"turn_ref"`
	Summary       string    `json:"summary"`
	CreatedAt     time.Time `json:"created_at"`
}

// Turn kinds. Kept as constants so the UI/Eval never hard-codes strings.
const (
	TurnPlanner = "planner"
	TurnWork    = "work"
	TurnSystem  = "system"
	// TurnHarness is one harness conversation turn (HARNESS runtime: one agent per
	// task, no planner/worker pipeline).
	TurnHarness = "harness"
)

// Truncate revision summaries so a chatty agent cannot bloat the table.
const harnessSummaryMax = 500

// NextRevision allocates the next revision for a task and records the Turn
// boundary in one transaction, bumping tasks.revision to the same value.
//
// Serialization: the UPDATE ... RETURNING on tasks takes the row lock, so two
// concurrent Turn ends can never receive the same revision. This is the same
// single-statement CAS style the engine already uses for intent claiming.
//
// A task that no longer exists (deleted mid-run) is not an error condition the
// caller must handle: it returns (0, nil) so the engine's settle path does not
// start failing tasks that were simply deleted underneath it.
func (d *DB) NextRevision(ctx context.Context, taskID, explorationID int64, turnKind, turnRef, summary string) (int64, error) {
	if d == nil || taskID <= 0 {
		return 0, nil
	}
	if len(summary) > harnessSummaryMax {
		summary = truncateUTF8Safe(summary, harnessSummaryMax)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var rev int64
	err = tx.QueryRowContext(ctx,
		`UPDATE tasks SET revision = revision + 1 WHERE id = $1 RETURNING revision`,
		taskID).Scan(&rev)
	if err == sql.ErrNoRows {
		return 0, nil // task deleted underneath the run
	}
	if err != nil {
		return 0, fmt.Errorf("bump task revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO harness_revisions(task_id, exploration_id, revision, turn_kind, turn_ref, summary)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (task_id, revision) DO NOTHING`,
		taskID, explorationID, rev, turnKind, turnRef, summary); err != nil {
		return 0, fmt.Errorf("record revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rev, nil
}

// TaskRevision reports the task's current revision (0 when unknown).
func (d *DB) TaskRevision(taskID int64) int64 {
	if d == nil || taskID <= 0 {
		return 0
	}
	var rev int64
	if err := d.QueryRow(`SELECT revision FROM tasks WHERE id=$1`, taskID).Scan(&rev); err != nil {
		return 0
	}
	return rev
}

// ListRevisions returns Turn boundaries for a task with revision > since,
// oldest first (the order a replay or a client catch-up wants).
func (d *DB) ListRevisions(taskID, since int64, limit int) ([]HarnessRevision, error) {
	if d == nil || taskID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := d.Query(`
SELECT id, task_id, exploration_id, revision, turn_kind, turn_ref, summary, created_at
FROM harness_revisions
WHERE task_id=$1 AND revision > $2
ORDER BY revision ASC
LIMIT $3`, taskID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]HarnessRevision, 0, 16)
	for rows.Next() {
		var r HarnessRevision
		if err := rows.Scan(&r.ID, &r.TaskID, &r.ExplorationID, &r.Revision,
			&r.TurnKind, &r.TurnRef, &r.Summary, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevisionRange returns the revision bounds of a task (0,0 when there are none).
func (d *DB) RevisionRange(taskID int64) (min, max int64, err error) {
	if d == nil || taskID <= 0 {
		return 0, 0, nil
	}
	err = d.QueryRow(`SELECT COALESCE(MIN(revision),0), COALESCE(MAX(revision),0)
FROM harness_revisions WHERE task_id=$1`, taskID).Scan(&min, &max)
	return min, max, err
}

// truncateUTF8Safe cuts s to at most n bytes without splitting a rune.
func truncateUTF8Safe(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// RecordTurnItem links one activity row to the Turn it was emitted in. Best
// effort: a failure loses replay precision for that item, never the item itself.
func (d *DB) RecordTurnItem(taskID, revision, activityID int64) error {
	if d == nil || taskID <= 0 || revision <= 0 || activityID <= 0 {
		return nil
	}
	_, err := d.Exec(`INSERT INTO harness_turn_items(task_id, revision, activity_id)
VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, taskID, revision, activityID)
	return err
}

// ActivityIDsForRevisions returns the activity ids belonging to turns with
// revision > since, oldest first. Used by the envelope stream's catch-up and by
// replay.
func (d *DB) ActivityIDsForRevisions(taskID, since int64, limit int) ([]int64, error) {
	if d == nil || taskID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := d.Query(`SELECT activity_id FROM harness_turn_items
WHERE task_id=$1 AND revision > $2 ORDER BY activity_id ASC LIMIT $3`, taskID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0, 64)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TurnOfActivity maps one activity id back to its revision (0 when unmapped —
// i.e. the row predates revisions or was emitted outside a turn).
func (d *DB) TurnOfActivity(taskID, activityID int64) int64 {
	if d == nil || taskID <= 0 || activityID <= 0 {
		return 0
	}
	var rev int64
	if err := d.QueryRow(`SELECT revision FROM harness_turn_items
WHERE task_id=$1 AND activity_id=$2 LIMIT 1`, taskID, activityID).Scan(&rev); err != nil {
		return 0
	}
	return rev
}

// TurnItem is one (revision, activity) pair — the join needed to rebuild a run.
type TurnItem struct {
	Revision   int64 `json:"revision"`
	ActivityID int64 `json:"activity_id"`
}

// TurnItemsInRange returns the (revision, activity) pairs whose revision falls in
// [from, to]. Used by replay to rebuild a run without N queries per item.
func (d *DB) TurnItemsInRange(taskID, from, to int64, limit int) ([]TurnItem, error) {
	if d == nil || taskID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 20000 {
		limit = 5000
	}
	if to <= 0 {
		to = 1<<62 - 1
	}
	rows, err := d.Query(`SELECT revision, activity_id FROM harness_turn_items
WHERE task_id=$1 AND revision >= $2 AND revision <= $3
ORDER BY activity_id ASC LIMIT $4`, taskID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TurnItem, 0, 256)
	for rows.Next() {
		var t TurnItem
		if err := rows.Scan(&t.Revision, &t.ActivityID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
