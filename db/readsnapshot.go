// Read-snapshot support (harness S1).
//
// graph_overview renders a situational picture of a task by issuing ~10 separate
// queries. Before this file those queries each went to the pool independently, so
// with 3 workers + 1 planner round running concurrently the picture could be a
// mix of two states: an edge written but its nodes not yet, a fact counted in
// "facts" but missing from "recent_facts", frontier counted before a claim landed.
// A planner that reasons on a half-updated graph dispatches intents against a
// world that never existed.
//
// WithSnapshot pins every read in the callback to ONE PostgreSQL transaction at
// REPEATABLE READ, READ ONLY, so all queries observe a single consistent MVCC
// snapshot. Read-only is deliberate: a snapshot must never be able to write.
//
// Scope: exploration/graph reads (exploration.go, digest.go,
// exploration_sources.go, constraints.go, intent_admission.go, task_context.go)
// and asset reads (assets.go, asset_dsl.go, task_scope.go, task_assets*.go).
// Cross-task blackboard reads (relatedTaskOverviews) deliberately stay outside:
// they are documented as a live view of OTHER tasks, not part of this task's
// snapshot.
package db

import (
	"context"
	"database/sql"
)

// querier is the subset of *sql.DB / *sql.Tx the read paths need. Routing reads
// through this interface is what makes a snapshot possible without rewriting
// every call site to take a tx argument.
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// q returns the executor a READ must use: the bound snapshot transaction when
// one is active, else the shared pool.
//
// Writes intentionally do NOT go through q: a snapshot is READ ONLY, and write
// paths already manage their own transactions (AssetStore.tx).
func (s *ExplorationStore) q() querier {
	if s.tx != nil {
		return s.tx
	}
	return s.db.DB
}

// q is the read-side counterpart of ExplorationStore.q. It uses readTx (set only
// by WithReadSnapshot), never the write tx, so binding a read snapshot cannot
// silently fold a write into a read-only transaction.
func (s *AssetStore) q() querier {
	if s.readTx != nil {
		return s.readTx
	}
	return s.db.DB
}

// WithReadSnapshot runs fn with every ExplorationStore read pinned to one
// REPEATABLE READ, READ ONLY transaction.
//
// The transaction is always rolled back (nothing is written); its only purpose is
// to hold a stable MVCC snapshot. A nil store keeps the callback working on the
// pool so callers don't need a nil check (mirrors the rest of the store's
// nil-safe surface).
func (s *ExplorationStore) WithReadSnapshot(ctx context.Context, fn func() error) error {
	if s == nil || s.db == nil {
		return fn()
	}
	// Serialize snapshots on this store.
	//
	// The snapshot is carried by a mutable field (s.tx) because every read path already
	// goes through q(); two concurrent callers on the SAME store would therefore swap
	// the field under each other, and the loser would issue queries on a transaction the
	// winner had already rolled back — which surfaced as a pgx panic
	// ("index out of range [0] with length 0") once two consumers rendered the graph at
	// the same time. One snapshot at a time per store is enough: it is a read of a few
	// hundred rows.
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		// Falling back to the pool keeps graph_overview available when the
		// server is saturated; the caller still gets a (possibly torn) picture
		// rather than a hard failure. The reason is reported so it shows up in
		// the log rather than being silent.
		return &SnapshotUnavailableError{Err: err}
	}
	prev := s.tx
	s.tx = tx
	defer func() {
		s.tx = prev
		_ = tx.Rollback()
	}()
	return fn()
}

// WithReadSnapshot is the asset-side counterpart: it pins AssetStore reads to the
// same kind of read-only snapshot. Used together with ExplorationStore's version
// when a renderer needs both graphs to agree.
func (s *AssetStore) WithReadSnapshot(ctx context.Context, fn func() error) error {
	if s == nil || s.db == nil {
		return fn()
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return &SnapshotUnavailableError{Err: err}
	}
	prev := s.readTx
	s.readTx = tx
	defer func() {
		s.readTx = prev
		_ = tx.Rollback()
	}()
	return fn()
}

// SnapshotUnavailableError reports that a read snapshot could not be opened, so
// the caller knows its result may be torn. It is a distinct type so callers can
// choose to degrade (continue on the pool) or fail loudly.
type SnapshotUnavailableError struct{ Err error }

func (e *SnapshotUnavailableError) Error() string {
	return "read snapshot unavailable: " + e.Err.Error()
}

func (e *SnapshotUnavailableError) Unwrap() error { return e.Err }
