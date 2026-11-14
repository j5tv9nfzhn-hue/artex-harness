// Evidence integrity reporting (harness S4).
//
// Upstream already stores findings, ordered traffic-evidence bindings and
// content-addressed snapshots. What it never answers is the question an operator
// actually asks before shipping a report: "which of these conclusions have real
// evidence behind them?".
//
// This is a REPORT, not a gate. Decision 5 (docs/adr/0003) is explicit: no new hard
// constraints, no full approval. So the harness surfaces the gap and lets the human
// decide — consistent with docs/02 §3.5's finding that prompt-level rules without
// deterministic enforcement are decoration, while enforcement the operator did not
// ask for is friction.
package db

import (
	"context"
	"fmt"
)

// EvidenceRow is one finding's evidence status inside a task.
type EvidenceRow struct {
	FindingID int64  `json:"finding_id"`
	NodeID    int64  `json:"node_id,omitempty"`
	Name      string `json:"name"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
	Bindings  int    `json:"bindings"`
	// Snapshots counts DISTINCT snapshots, which can differ from Bindings only in
	// corrupt data; it is reported so a mismatch is visible rather than hidden.
	Snapshots int  `json:"snapshots"`
	HasReport bool `json:"has_report"`
}

// EvidenceIntegrity is the per-task summary.
type EvidenceIntegrity struct {
	TaskID    int64         `json:"task_id"`
	Total     int           `json:"total"`
	WithProof int           `json:"with_evidence"`
	Without   int           `json:"without_evidence"`
	Rows      []EvidenceRow `json:"rows"`
	// Note is rendered by the UI verbatim; it states plainly that this is advisory.
	Note string `json:"note"`
}

// FindingEvidenceIntegrity collects the evidence status of every finding of a task.
//
// A finding counts as "has evidence" when at least one traffic binding exists. No
// judgement is made about whether the evidence actually supports the claim — that
// is the retester's / the human's job.
func (d *DB) FindingEvidenceIntegrity(ctx context.Context, taskID int64) (*EvidenceIntegrity, error) {
	out := &EvidenceIntegrity{
		TaskID: taskID,
		Rows:   make([]EvidenceRow, 0, 16),
		Note: "仅供参考：这里只统计『有没有绑定流量证据』，不判断证据是否足以支撑结论。" +
			"未绑定的漏洞不阻止提交或导出；TCP 类、未开启采集、无法确切匹配时省略绑定是允许的。",
	}
	if d == nil || taskID <= 0 {
		return out, nil
	}
	rows, err := d.QueryContext(ctx, `
SELECT f.id,
       COALESCE(f.node_id, 0),
       COALESCE(NULLIF(f.name,''), NULLIF(f.vulnclass,''), '(未命名)'),
       COALESCE(f.severity,''),
       COALESCE(f.status,''),
       (SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id = f.id),
       (SELECT count(DISTINCT b.snapshot_id) FROM finding_traffic_bindings b WHERE b.finding_id = f.id),
       (COALESCE(f.report,'') <> '')
FROM findings f
WHERE f.task_id = $1
ORDER BY f.id ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("finding evidence integrity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r EvidenceRow
		if err := rows.Scan(&r.FindingID, &r.NodeID, &r.Name, &r.Severity,
			&r.Status, &r.Bindings, &r.Snapshots, &r.HasReport); err != nil {
			return nil, err
		}
		if r.Bindings > 0 {
			out.WithProof++
		} else {
			out.Without++
		}
		out.Rows = append(out.Rows, r)
	}
	out.Total = len(out.Rows)
	return out, rows.Err()
}
