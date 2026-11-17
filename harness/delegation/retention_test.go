package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A restart must keep the audit trail: what a previous process recorded is still
// listed, and a record that was "running" when the process died comes back
// failed with an explicit reason (nothing is running to finish it).
func TestRestoreKeepsTrailAndFailsInterruptedRuns(t *testing.T) {
	dir := t.TempDir()

	// First process: one finished delegation and one still running.
	r1 := New(dir)
	done := r1.Start("recon", "scan", "", "", false, time.Unix(1000, 0))
	r1.Finish(done, StatusDone, "host a", "")

	live := r1.Start("verify", "check", "", "", true, time.Unix(2000, 0))
	r1.SetAgentID(live, "agent-live")
	r1.SetWorkDir(live, filepath.Join(dir, live))

	// Second process: a fresh registry over the same directory.
	r2 := New(dir)
	n, err := r2.Restore()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n != 2 {
		t.Fatalf("restored %d records, want 2", n)
	}

	got, ok := r2.Get(done)
	if !ok || got.Status != StatusDone || got.Result != "host a" {
		t.Fatalf("finished record not restored: %+v (ok=%v)", got, ok)
	}

	interrupted, ok := r2.Get(live)
	if !ok {
		t.Fatal("running record was dropped instead of being marked interrupted")
	}
	if interrupted.Status != StatusFailed {
		t.Fatalf("interrupted status = %q, want failed", interrupted.Status)
	}
	if interrupted.Error == "" {
		t.Fatal("interrupted record must explain why it is failed")
	}
	if interrupted.AgentID != "agent-live" {
		t.Fatalf("agent id lost on restore: %q", interrupted.AgentID)
	}

	// Idempotent: restoring again must not duplicate.
	if n2, _ := r2.Restore(); n2 != 0 {
		t.Fatalf("second Restore added %d records, want 0", n2)
	}
	// Sequence continues past restored ids, so new ids never collide.
	next := r2.Start("recon", "again", "", "", false, time.Now())
	if next == done || next == live {
		t.Fatalf("id %q collides with a restored id", next)
	}
}

// A torn journal line (crash mid-append) must not lose the whole history.
func TestRestoreSkipsTornJournalLine(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	id := r.Start("recon", "scan", "", "", false, time.Unix(1000, 0))
	r.Finish(id, StatusDone, "ok", "")

	path := filepath.Join(dir, "delegations.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"d9","agent_type":"rec`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	r2 := New(dir)
	if _, err := r2.Restore(); err != nil {
		t.Fatalf("a torn line must not fail the restore: %v", err)
	}
	if _, ok := r2.Get(id); !ok {
		t.Fatal("complete record lost because of a torn neighbour")
	}
}

// Retention: terminal delegations past the TTL lose their isolated directory but
// keep their record; running ones and the result body are untouched.
func TestSweepReclaimsOldDirectoriesAndKeepsRecords(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)

	oldID := r.Start("recon", "old", "", "", true, time.Now().Add(-72*time.Hour))
	oldDir := filepath.Join(dir, oldID)
	if err := os.MkdirAll(filepath.Join(oldDir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "scratch", "nmap.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.SetWorkDir(oldID, oldDir)
	body, err := r.WriteOutput(oldID, "result body")
	if err != nil {
		t.Fatal(err)
	}
	r.Finish(oldID, StatusDone, "result body", "")

	// A fresh terminal delegation: must survive.
	freshID := r.Start("recon", "fresh", "", "", true, time.Now())
	freshDir := filepath.Join(dir, freshID)
	if err := os.MkdirAll(freshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.SetWorkDir(freshID, freshDir)
	r.Finish(freshID, StatusDone, "fresh", "")

	// A running delegation: never touched, however old.
	runID := r.Start("verify", "running", "", "", true, time.Now().Add(-72*time.Hour))
	runDir := filepath.Join(dir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.SetWorkDir(runID, runDir)

	// Finish stamps EndedAt with the real clock. Age the old record through the
	// journal (the same shape a delegation recorded days ago has on disk) and
	// read it back — so the sweep sees a genuinely old END time, not just an old
	// START time.
	ageJournalEndedAt(t, dir, oldID, time.Now().Add(-72*time.Hour))
	r2 := New(dir)
	if _, err := r2.Restore(); err != nil {
		t.Fatal(err)
	}

	// Window: sweep relative to now, well past the old one's end. The still-running
	// record comes back terminal after a restart (nothing is running), and its
	// start is 72h old, so it is legitimately reclaimable here too — the
	// "running is never swept" invariant is covered by TestSweepNeverTouchesRunning.
	st := r2.Sweep(time.Now(), 24*time.Hour)
	if st.Reclaimed != 2 {
		t.Fatalf("reclaimed %d, want 2 (stats %+v)", st.Reclaimed, st)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("old isolated directory still on disk")
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatal("fresh delegation directory was removed too early")
	}
	// The result body is the evidence; it must never be swept.
	if _, err := os.Stat(body); err != nil {
		t.Fatal("result body removed by the retention sweep")
	}
	rec, ok := r2.Get(oldID)
	if !ok || !rec.Reclaimed || rec.WorkDir != "" {
		t.Fatalf("record = %+v, ok=%v; want reclaimed with empty work dir", rec, ok)
	}
	if _, ok := r2.Get(freshID); !ok {
		t.Fatal("fresh record lost")
	}
}

// A running delegation is never swept, however old its start time.
func TestSweepNeverTouchesRunning(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	id := r.Start("verify", "running", "", "", true, time.Now().Add(-72*time.Hour))
	runDir := filepath.Join(dir, id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.SetWorkDir(id, runDir)

	st := r.Sweep(time.Now(), time.Hour)
	if st.Reclaimed != 0 {
		t.Fatalf("reclaimed %d running delegations, want 0 (stats %+v)", st.Reclaimed, st)
	}
	if _, err := os.Stat(runDir); err != nil {
		t.Fatal("running delegation directory was removed")
	}
	if rec, ok := r.Get(id); !ok || rec.Status != StatusRunning || rec.Reclaimed {
		t.Fatalf("record = %+v, ok=%v; a running delegation must stay untouched", rec, ok)
	}
}

// ageJournalEndedAt rewrites one record's ended_at in the journal, producing the
// on-disk state of a delegation that finished long ago.
func ageJournalEndedAt(t *testing.T, dir, id string, ended time.Time) {
	t.Helper()
	path := filepath.Join(dir, "delegations.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.ID == id {
			rec.EndedAt = ended
			rec.DurationMS = rec.EndedAt.Sub(rec.StartedAt).Milliseconds()
			if b, err := json.Marshal(rec); err == nil {
				line = string(b)
			}
			found = true
		}
		out = append(out, line)
	}
	if !found {
		t.Fatalf("record %q not found in journal", id)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The sweep refuses to delete anything outside the delegation store, even if a
// record's WorkDir was somehow set to the task workspace or "/".
func TestSweepRefusesPathsOutsideStore(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	r := New(dir)
	id := r.Start("recon", "x", "", "", true, time.Unix(1000, 0))
	r.SetWorkDir(id, outside)
	r.Finish(id, StatusDone, "ok", "")

	if st := r.Sweep(time.Unix(1000, 0).Add(100*time.Hour), time.Hour); st.Reclaimed != 0 {
		t.Fatalf("reclaimed %d outside paths, want 0", st.Reclaimed)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("sweep deleted a directory outside the delegation store")
	}
}
