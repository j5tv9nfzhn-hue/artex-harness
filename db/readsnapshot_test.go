package db

import (
	"context"
	"os"
	"testing"
	"time"
)

// These tests need a real PostgreSQL (REPEATABLE READ snapshot semantics cannot be
// faked). They self-skip when no DSN is configured, matching the rest of the db
// suite (see testmain_test.go).

func testDB(t *testing.T) *DB {
	t.Helper()
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("no PostgreSQL DSN configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("cannot open PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestReadSnapshotIsolatesConcurrentWrites is the S1 acceptance test for the
// graph_overview consistency fix: a snapshot opened BEFORE a concurrent write must
// not observe that write, and a read taken after must.
//
// This is the exact failure mode the fix targets — graph_overview issuing ~10
// independent queries while N workers write the same graph.
func TestReadSnapshotIsolatesConcurrentWrites(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()

	expID, err := d.CreateExploration("harness snapshot test", "snapshot isolation")
	if err != nil {
		t.Fatalf("create exploration: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) })

	store := d.Exploration(expID)
	// One node exists before the snapshot opens.
	if _, err := store.AddNode(KindFact, map[string]any{"summary": "before"}, 0, "confirmed", "test", nil); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	var inSnapshot, afterWrite int
	err = store.WithReadSnapshot(ctx, func() error {
		first, err := countFacts(store)
		if err != nil {
			return err
		}
		inSnapshot = first

		// A concurrent writer commits a second node while the snapshot is open.
		// A fresh store (no bound tx) writes on the pool, like an engine worker.
		if _, err := d.Exploration(expID).AddNode(KindFact, map[string]any{"summary": "during"}, 0, "confirmed", "test", nil); err != nil {
			return err
		}
		// Re-reading inside the SAME snapshot must still show the old count.
		second, err := countFacts(store)
		if err != nil {
			return err
		}
		afterWrite = second
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if inSnapshot != 1 {
		t.Fatalf("snapshot saw %d facts before the write, want 1", inSnapshot)
	}
	if afterWrite != inSnapshot {
		t.Fatalf("snapshot was torn: %d → %d after a concurrent commit (REPEATABLE READ must hold it at %d)",
			inSnapshot, afterWrite, inSnapshot)
	}
	// Outside the snapshot the new row is visible, proving it really committed.
	if n, err := countFacts(store); err != nil || n != 2 {
		t.Fatalf("post-snapshot read = %d (err=%v), want 2", n, err)
	}
}

// TestReadSnapshotIsReadOnly asserts the snapshot cannot write: a snapshot that
// silently allowed writes would be a correctness hazard (a "read" path mutating
// state behind the caller's back).
func TestReadSnapshotIsReadOnly(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	expID, err := d.CreateExploration("harness snapshot ro", "read only")
	if err != nil {
		t.Fatalf("create exploration: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) })
	store := d.Exploration(expID)

	_ = store.WithReadSnapshot(ctx, func() error {
		var id int64
		err := store.q().QueryRow(`INSERT INTO exploration_nodes(exploration_id, kind, state)
VALUES ($1,'fact','confirmed') RETURNING id`, expID).Scan(&id)
		if err == nil {
			t.Fatalf("write inside a read-only snapshot succeeded; it must be rejected")
		}
		return nil
	})
}

// TestNextRevisionIsMonotonic covers the other half of S1: revisions must be
// unique and increasing per task even under concurrency (that is what makes them
// usable as an ordering key).
func TestNextRevisionIsMonotonic(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()

	var expID int64
	if err := d.QueryRow(`INSERT INTO explorations(description, goal) VALUES ('rev test','rev') RETURNING id`).Scan(&expID); err != nil {
		t.Fatalf("exploration: %v", err)
	}
	var taskID int64
	if err := d.QueryRow(`INSERT INTO tasks(description, goal, exploration_id, status)
VALUES ('rev test','rev',$1,'running') RETURNING id`, expID).Scan(&taskID); err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, taskID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	const n = 25
	got := make(chan int64, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			rev, err := d.NextRevision(ctx, taskID, expID, TurnWork, "work#test", "concurrent")
			if err != nil {
				t.Errorf("NextRevision: %v", err)
				got <- 0
				return
			}
			got <- rev
		}(i)
	}
	seen := map[int64]bool{}
	for i := 0; i < n; i++ {
		rev := <-got
		if rev == 0 {
			t.Fatalf("revision not allocated")
		}
		if seen[rev] {
			t.Fatalf("duplicate revision %d allocated concurrently", rev)
		}
		seen[rev] = true
	}
	// tasks.revision must equal the highest allocated revision.
	if cur := d.TaskRevision(taskID); cur != n {
		t.Fatalf("tasks.revision = %d, want %d", cur, n)
	}
	// Boundaries are persisted and ordered.
	rows, err := d.ListRevisions(taskID, 0, 100)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("recorded %d turns, want %d", len(rows), n)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Revision <= rows[i-1].Revision {
			t.Fatalf("revisions not strictly increasing: %d then %d", rows[i-1].Revision, rows[i].Revision)
		}
	}
}

// TestTurnItemMapping covers revision↔activity linkage used by replay.
func TestTurnItemMapping(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	var expID int64
	if err := d.QueryRow(`INSERT INTO explorations(description, goal) VALUES ('map test','map') RETURNING id`).Scan(&expID); err != nil {
		t.Fatalf("exploration: %v", err)
	}
	var taskID int64
	if err := d.QueryRow(`INSERT INTO tasks(description, goal, exploration_id, status)
VALUES ('map test','map',$1,'running') RETURNING id`, expID).Scan(&taskID); err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, taskID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	store := d.Exploration(expID)
	rev, err := d.NextRevision(ctx, taskID, expID, TurnPlanner, "planner#1", "第 1 轮规划")
	if err != nil {
		t.Fatalf("NextRevision: %v", err)
	}
	a := Activity{Kind: "text", Summary: "hello", CreatedAt: time.Now()}
	id, err := store.AppendActivity(a)
	if err != nil {
		t.Fatalf("AppendActivity: %v", err)
	}
	if err := d.RecordTurnItem(taskID, rev, id); err != nil {
		t.Fatalf("RecordTurnItem: %v", err)
	}
	if got := d.TurnOfActivity(taskID, id); got != rev {
		t.Fatalf("TurnOfActivity = %d, want %d", got, rev)
	}
	ids, err := d.ActivityIDsForRevisions(taskID, rev-1, 10)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("ActivityIDsForRevisions = %v (err=%v)", ids, err)
	}
	// Asking for turns after rev returns nothing.
	if ids, _ := d.ActivityIDsForRevisions(taskID, rev, 10); len(ids) != 0 {
		t.Fatalf("expected no items after rev %d, got %v", rev, ids)
	}
}

// TestWithReadSnapshotWithoutDBEnsuresNilSafety guards the degradation path: a
// store with no DB must still run the callback instead of panicking, because
// graph_overview is also rendered in unit tests with nil stores.
func TestWithReadSnapshotWithoutDBEnsuresNilSafety(t *testing.T) {
	var s *ExplorationStore
	ran := false
	if err := s.WithReadSnapshot(context.Background(), func() error { ran = true; return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ran {
		t.Fatal("callback did not run for a nil store")
	}
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Log("no DSN: snapshot isolation itself is covered by the tests above when a DB is present")
	}
}

// countFacts counts fact nodes through the store's read executor, so it observes
// whatever snapshot the store is currently bound to.
func countFacts(s *ExplorationStore) (int, error) {
	var n int
	err := s.q().QueryRow(`SELECT count(*) FROM exploration_nodes
WHERE exploration_id=$1 AND kind='fact'`, s.expID).Scan(&n)
	return n, err
}
