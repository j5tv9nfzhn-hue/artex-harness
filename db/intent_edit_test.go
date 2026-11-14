package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// helper: a fresh exploration with an optional number of open intents.
func newEditFixture(t *testing.T) (*DB, *ExplorationStore, int64) {
	t.Helper()
	d := testDB(t)
	expID, err := d.CreateExploration("intent edit test", "edit intents")
	if err != nil {
		t.Fatalf("create exploration: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) })
	return d, d.Exploration(expID), expID
}

func addIntent(t *testing.T, s *ExplorationStore, summary string, prio int) int64 {
	t.Helper()
	id, err := s.AddNode(KindIntent, map[string]any{
		"summary":    summary,
		"asset_ids":  []int64{1, 2},
		"parent_ids": []int64{7},
	}, prio, "open", "planner", nil)
	if err != nil {
		t.Fatalf("add intent: %v", err)
	}
	return id
}

func payloadMap(t *testing.T, n *Node) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(n.Payload, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return p
}

// TestUpdateIntentContentEditIsAPatch is the core S2 guarantee: editing a summary
// must NOT silently drop the other keys (parent_ids carries the lineage the
// planner relies on for dedup).
func TestUpdateIntentContentEditIsAPatch(t *testing.T) {
	_, store, _ := newEditFixture(t)
	id := addIntent(t, store, "探一下 /admin", 0)

	newSummary := "探 /admin 的 SSRF 入口（人工修正）"
	node, err := store.UpdateIntentContent(id, IntentPatch{Summary: &newSummary})
	if err != nil {
		t.Fatalf("UpdateIntentContent: %v", err)
	}
	p := payloadMap(t, node)
	if p["summary"] != newSummary {
		t.Fatalf("summary = %v", p["summary"])
	}
	if p["edited_by"] != "human" {
		t.Fatalf("provenance marker missing: %v", p["edited_by"])
	}
	if p["parent_ids"] == nil {
		t.Fatalf("parent_ids was dropped by a summary-only patch: %v", p)
	}
	if node.Priority != 0 {
		t.Fatalf("priority changed by a summary-only patch: %d", node.Priority)
	}
}

// TestUpdateIntentContentReplacesAssetIDs covers the re-scoping case and asserts
// the legacy singular keys cannot shadow the new list (compactIntents falls back
// to target_id/target_ids when asset_ids is absent).
func TestUpdateIntentContentReplacesAssetIDs(t *testing.T) {
	d, store, _ := newEditFixture(t)
	id, err := store.AddNode(KindIntent, map[string]any{
		"summary":    "legacy intent",
		"target_id":  "9",
		"target_ids": []int{9},
	}, 0, "open", "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{11, 12}
	node, err := store.UpdateIntentContent(id, IntentPatch{AssetIDs: &ids})
	if err != nil {
		t.Fatalf("UpdateIntentContent: %v", err)
	}
	p := payloadMap(t, node)
	if _, still := p["target_id"]; still {
		t.Fatalf("legacy target_id survived: %v", p)
	}
	if _, still := p["target_ids"]; still {
		t.Fatalf("legacy target_ids survived: %v", p)
	}
	arr, ok := p["asset_ids"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("asset_ids = %v", p["asset_ids"])
	}
	_ = d
}

// TestUpdateIntentContentBumpsContentVersion: cold-digest decides whether a folded
// node's digest body is stale by comparing content_version; an edit that did not
// bump it would leave a stale summary rendered forever.
func TestUpdateIntentContentBumpsContentVersion(t *testing.T) {
	_, store, expID := newEditFixture(t)
	id := addIntent(t, store, "before", 0)
	before := contentVersion(t, store, expID, id)
	s := "after"
	if _, err := store.UpdateIntentContent(id, IntentPatch{Summary: &s}); err != nil {
		t.Fatal(err)
	}
	after := contentVersion(t, store, expID, id)
	if after <= before {
		t.Fatalf("content_version did not advance: %d → %d", before, after)
	}
}

func contentVersion(t *testing.T, s *ExplorationStore, expID, id int64) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow(`SELECT content_version FROM exploration_nodes WHERE id=$1 AND exploration_id=$2`, id, expID).Scan(&v); err != nil {
		t.Fatalf("content_version: %v", err)
	}
	return v
}

// TestUpdateIntentContentPriority checks the column (not payload) path, including
// that 0 is a real value rather than "leave unchanged".
func TestUpdateIntentContentPriority(t *testing.T) {
	_, store, _ := newEditFixture(t)
	id := addIntent(t, store, "prio", 5)
	zero := 0
	node, err := store.UpdateIntentContent(id, IntentPatch{Priority: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if node.Priority != 0 {
		t.Fatalf("priority = %d, want 0", node.Priority)
	}
	seven := 7
	if node, err = store.UpdateIntentContent(id, IntentPatch{Priority: &seven}); err != nil {
		t.Fatal(err)
	}
	if node.Priority != 7 {
		t.Fatalf("priority = %d, want 7", node.Priority)
	}
}

// TestUpdateIntentContentRejects covers every guard so a bad caller gets a typed
// error instead of a silent no-op.
func TestUpdateIntentContentRejects(t *testing.T) {
	_, store, _ := newEditFixture(t)
	id := addIntent(t, store, "guarded", 0)

	if _, err := store.UpdateIntentContent(id, IntentPatch{}); !errors.Is(err, ErrIntentEditEmpty) {
		t.Fatalf("empty patch: %v", err)
	}
	blank := "   "
	if _, err := store.UpdateIntentContent(id, IntentPatch{Summary: &blank}); !errors.Is(err, ErrIntentNotEditable) {
		t.Fatalf("blank summary: %v", err)
	}
	longRunes := make([]rune, 0, MaxIntentSummaryRunes+1)
	for i := 0; i <= MaxIntentSummaryRunes; i++ {
		longRunes = append(longRunes, 'x')
	}
	long := string(longRunes)
	if _, err := store.UpdateIntentContent(id, IntentPatch{Summary: &long}); !errors.Is(err, ErrIntentNotEditable) {
		t.Fatalf("oversized summary: %v", err)
	}
	// A node that is not an intent is not editable.
	factID, err := store.AddNode(KindFact, map[string]any{"summary": "f"}, 0, "confirmed", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := "nope"
	if _, err := store.UpdateIntentContent(factID, IntentPatch{Summary: &s}); !errors.Is(err, ErrIntentNotEditable) {
		t.Fatalf("non-intent should not be editable: %v", err)
	}
}

// TestUpdateIntentContentRejectsDeletedIntent: a soft-deleted intent dropped out of
// the frontier on purpose; editing it must not put it back in play.
func TestUpdateIntentContentRejectsDeletedIntent(t *testing.T) {
	_, store, _ := newEditFixture(t)
	id := addIntent(t, store, "to delete", 0)
	if _, err := store.SoftDeleteIntent(id, "人工删除"); err != nil {
		t.Fatalf("SoftDeleteIntent: %v", err)
	}
	s := "resurrect?"
	if _, err := store.UpdateIntentContent(id, IntentPatch{Summary: &s}); !errors.Is(err, ErrIntentNotEditable) {
		t.Fatalf("deleted intent must not be editable: %v", err)
	}
}

// TestOrderFrontierRewritesClaimOrder is the ordering primitive's acceptance test:
// after reordering, the frontier (the exact query workers claim from) must return
// the requested order.
func TestOrderFrontierRewritesClaimOrder(t *testing.T) {
	_, store, _ := newEditFixture(t)
	a := addIntent(t, store, "A", 0)
	b := addIntent(t, store, "B", 0)
	c := addIntent(t, store, "C", 0)

	// Default order is priority DESC, id ASC → A, B, C.
	assertFrontierOrder(t, store, []int64{a, b, c})

	// Ask for C, A, B.
	if _, err := store.OrderFrontier(context.Background(), []int64{c, a, b}); err != nil {
		t.Fatalf("OrderFrontier: %v", err)
	}
	assertFrontierOrder(t, store, []int64{c, a, b})
}

func assertFrontierOrder(t *testing.T, store *ExplorationStore, want []int64) {
	t.Helper()
	fr, err := store.Frontier(10)
	if err != nil {
		t.Fatalf("Frontier: %v", err)
	}
	got := make([]int64, 0, len(fr))
	for _, n := range fr {
		got = append(got, n.ID)
	}
	if len(got) != len(want) {
		t.Fatalf("frontier = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frontier order = %v, want %v", got, want)
		}
	}
}

// TestOrderFrontierOnlyTouchesThisExploration: an id from another exploration must
// not be reordered (the UPDATE is scoped by exploration_id).
func TestOrderFrontierOnlyTouchesThisExploration(t *testing.T) {
	d := testDB(t)
	expA, err := d.CreateExploration("order A", "a")
	if err != nil {
		t.Fatal(err)
	}
	expB, err := d.CreateExploration("order B", "b")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expA)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expB)
	})
	a := d.Exploration(expA)
	bStore := d.Exploration(expB)
	own := addIntent(t, a, "own", 0)
	foreign := addIntent(t, bStore, "foreign", 0)

	if _, err := a.OrderFrontier(context.Background(), []int64{foreign, own}); err != nil {
		t.Fatalf("OrderFrontier: %v", err)
	}
	// A's own intent got the top slot; the foreign one is untouched.
	fr, err := a.Frontier(10)
	if err != nil || len(fr) != 1 || fr[0].ID != own {
		t.Fatalf("frontier A = %v (err=%v)", fr, err)
	}
	nf, err := bStore.GetNode(foreign)
	if err != nil || nf == nil {
		t.Fatalf("foreign node: %v", err)
	}
	if nf.Priority != 0 {
		t.Fatalf("foreign intent priority was rewritten: %d", nf.Priority)
	}
}
