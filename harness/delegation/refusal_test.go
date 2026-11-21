package delegation

import (
	"testing"
	"time"
)

// Refused must survive the journal round-trip: a restarted process lists the
// delegation as refused, not as an ordinary failure.
func TestFinishWithRefusedPersistsThroughRestore(t *testing.T) {
	dir := t.TempDir()
	reg := New(dir)
	id := reg.Start("recon", "p", "", "", false, time.Now())
	reg.FinishWithRefused(id, StatusFailed, "I'm sorry, but I can't help with that.", "", true)

	restored := New(dir)
	if _, err := restored.Restore(); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	rec, ok := restored.Get(id)
	if !ok {
		t.Fatal("record missing after restore")
	}
	if !rec.Refused {
		t.Fatal("Refused flag lost across journal restore")
	}
	if rec.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", rec.Status)
	}
}

// Plain Finish keeps the zero value: existing callers are unaffected.
func TestFinishDefaultsRefusedFalse(t *testing.T) {
	reg := New(t.TempDir())
	id := reg.Start("recon", "p", "", "", false, time.Now())
	reg.Finish(id, StatusDone, "ok", "")
	if rec, _ := reg.Get(id); rec.Refused {
		t.Fatal("Finish without refusal must leave Refused false")
	}
}
