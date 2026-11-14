package db

import (
	"context"
	"testing"
)

// TestFindingEvidenceIntegrity classifies findings by whether they have real
// traffic evidence bound — the report an operator needs before shipping a
// conclusion. It is deliberately advisory (no enforcement).
func TestFindingEvidenceIntegrity(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()

	var expID int64
	if err := d.QueryRow(`INSERT INTO explorations(description, goal) VALUES ('evidence','evidence') RETURNING id`).Scan(&expID); err != nil {
		t.Fatal(err)
	}
	var taskID int64
	if err := d.QueryRow(`INSERT INTO tasks(description, goal, exploration_id, status)
VALUES ('evidence','evidence',$1,'running') RETURNING id`, expID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, taskID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	// One finding with evidence + report, one without evidence.
	var withEv, withoutEv int64
	if err := d.QueryRow(`INSERT INTO findings(task_id, name, severity, status, report)
VALUES ($1,'SQLi in /login','high','confirmed','# report') RETURNING id`, taskID).Scan(&withEv); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO findings(task_id, vulnclass, severity, status)
VALUES ($1,'open-redirect','low','pending') RETURNING id`, taskID).Scan(&withoutEv); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO traffic_evidence_snapshots
(id, source_traffic_id, captured_at, url, method, status, req_head, resp_head, req_hash, resp_hash, req_len, resp_len)
VALUES ('snap-1','t-1',1,'http://t/login','POST',200,'req','resp','h1','h2',10,20)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO finding_traffic_bindings(finding_id, snapshot_id, role, position)
VALUES ($1,'snap-1','proof',0)`, withEv); err != nil {
		t.Fatal(err)
	}

	rep, err := d.FindingEvidenceIntegrity(ctx, taskID)
	if err != nil {
		t.Fatalf("integrity: %v", err)
	}
	if rep.Total != 2 || rep.WithProof != 1 || rep.Without != 1 {
		t.Fatalf("summary = total %d with %d without %d", rep.Total, rep.WithProof, rep.Without)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("rows = %d", len(rep.Rows))
	}
	// Rows are ordered by finding id, so the first is the evidenced one.
	first := rep.Rows[0]
	if first.FindingID != withEv || first.Bindings != 1 || first.Snapshots != 1 || !first.HasReport {
		t.Fatalf("evidenced row = %+v", first)
	}
	if first.Name == "" {
		t.Fatal("name should fall back to the readable title")
	}
	second := rep.Rows[1]
	if second.FindingID != withoutEv || second.Bindings != 0 || second.HasReport {
		t.Fatalf("unevidenced row = %+v", second)
	}
	// The fallback name comes from vulnclass when 'name' is empty.
	if second.Name != "open-redirect" {
		t.Fatalf("fallback name = %q", second.Name)
	}
	// The report must state plainly that it is advisory (decision 5).
	if rep.Note == "" {
		t.Fatal("report must carry its advisory note")
	}
}

// TestFindingEvidenceIntegrityUnknownTask: an unknown/deleted task yields an empty
// report rather than an error, so the console never shows a scary failure for a
// task that simply has no findings.
func TestFindingEvidenceIntegrityUnknownTask(t *testing.T) {
	d := testDB(t)
	rep, err := d.FindingEvidenceIntegrity(context.Background(), 987654321)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.Total != 0 || len(rep.Rows) != 0 {
		t.Fatalf("expected empty report: %+v", rep)
	}
}
