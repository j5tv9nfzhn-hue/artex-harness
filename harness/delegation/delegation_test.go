package delegation

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartFinishAndNotification(t *testing.T) {
	r := New(t.TempDir())
	id := r.Start("recon", "scan hosts", "recon pass", "", true, time.Unix(1000, 0))
	if id == "" {
		t.Fatal("empty delegation id")
	}
	path, err := r.WriteOutput(id, "host a\nhost b")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) == "" {
		t.Fatal("empty output path")
	}
	r.SetAgentID(id, "agent-abc")
	r.Finish(id, StatusDone, "host a\nhost b", "")

	rec, ok := r.Get(id)
	if !ok || rec.Status != StatusDone || rec.AgentID != "agent-abc" || rec.DurationMS < 0 {
		t.Fatalf("record = %+v, ok=%v", rec, ok)
	}
	notes := r.DrainNotifications()
	if len(notes) != 1 || notes[0].TaskID != id || notes[0].Status != StatusDone {
		t.Fatalf("notifications = %+v", notes)
	}
	if got := r.DrainNotifications(); len(got) != 0 {
		t.Fatal("DrainNotifications must clear")
	}
	if r.PeekNotifications() != nil && len(r.PeekNotifications()) != 0 {
		t.Fatal("Peek must not see drained notifications")
	}
}

func TestSynchronousDelegationDoesNotNotify(t *testing.T) {
	r := New(t.TempDir())
	id := r.Start("verify", "check", "", "", false, time.Time{})
	r.Finish(id, StatusDone, "confirmed", "")
	if got := r.DrainNotifications(); len(got) != 0 {
		t.Fatalf("a synchronous delegation must not enqueue a completion notification: %+v", got)
	}
}

func TestFinishIsIdempotent(t *testing.T) {
	r := New(t.TempDir())
	id := r.Start("recon", "x", "", "", true, time.Time{})
	r.Finish(id, StatusDone, "first", "")
	r.Finish(id, StatusFailed, "second", "boom")
	rec, _ := r.Get(id)
	if rec.Status != StatusDone || rec.Result != "first" || rec.Error != "" {
		t.Fatalf("second Finish overwrote the terminal state: %+v", rec)
	}
}

func TestKill(t *testing.T) {
	r := New(t.TempDir())
	id := r.Start("recon", "x", "", "", true, time.Time{})
	cancelled := false
	r.AttachCancel(id, func() { cancelled = true })
	if !r.Kill(id) {
		t.Fatal("Kill on a running delegation must succeed")
	}
	if !cancelled {
		t.Fatal("Kill must invoke the cancel func")
	}
	if r.Kill(id) {
		t.Fatal("Kill on a terminal delegation must fail")
	}
	rec, _ := r.Get(id)
	if rec.Status != StatusKilled {
		t.Fatalf("status = %q, want killed", rec.Status)
	}
	notes := r.DrainNotifications()
	if len(notes) != 1 || notes[0].Status != StatusKilled {
		t.Fatalf("a killed background delegation must notify: %+v", notes)
	}
}

func TestWriteOutputIsContentAddressed(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	a := r.Start("recon", "x", "", "", true, time.Time{})
	b := r.Start("recon", "y", "", "", true, time.Time{})
	pa, _ := r.WriteOutput(a, "same body")
	pb, _ := r.WriteOutput(b, "same body")
	if pa != pb {
		t.Fatal("identical outputs must share one file")
	}
}

func TestListAndSummary(t *testing.T) {
	r := New(t.TempDir())
	a := r.Start("recon", "a", "", "", true, time.Now().Add(-time.Second))
	r.Finish(a, StatusDone, "ok", "")
	b := r.Start("verify", "b", "", "", false, time.Now().Add(-2*time.Millisecond))
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("list = %d, want 2", len(list))
	}
	if list[0].Status != StatusDone || list[0].DurationMS < 0 {
		t.Fatalf("first record = %+v", list[0])
	}
	if list[1].Status != StatusRunning || list[1].DurationMS <= 0 {
		t.Fatalf("running record must report live elapsed time: %+v", list[1])
	}
	s := r.Summary()
	if s[StatusDone] != 1 || s[StatusRunning] != 1 {
		t.Fatalf("summary = %v", s)
	}
	if len(r.SortedIDs()) != 2 {
		t.Fatal("SortedIDs must cover all delegations")
	}
	_ = b
}

func TestGlobalRegistryIsShared(t *testing.T) {
	g := &global{regs: map[string]*Registry{}}
	r1 := g.For("task1", t.TempDir())
	r2 := g.For("task1", t.TempDir())
	if r1 != r2 {
		t.Fatal("Global.For must return the same registry for one task")
	}
	r1.Start("recon", "x", "", "", true, time.Time{})
	if len(g.For("task1", "").List()) != 1 {
		t.Fatal("HTTP and runtime must see the same delegations")
	}
	g.Drop("task1")
	if g.For("task1", "").List() != nil && len(g.For("task1", "").List()) != 0 {
		t.Fatal("Drop must clear the registry")
	}
}

func TestNotificationString(t *testing.T) {
	n := Notification{TaskID: "d1", AgentType: "recon", Status: StatusDone, OutputPath: "/tmp/o", Summary: "ok"}
	s := n.String()
	for _, want := range []string{"<task-notification>", "d1", "recon", "done", "/tmp/o"} {
		if !contains(s, want) {
			t.Fatalf("notification missing %q: %s", want, s)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || strings.Contains(s, sub))
}
