package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	actool "github.com/Autumn-27/norma/tool"
)

// fakeConn is a Conn that can be broken on demand, counting launches.
type fakeConn struct {
	name   string
	tools  []actool.CoreTool
	closed bool
}

func (f *fakeConn) Tools(context.Context) ([]actool.CoreTool, error) {
	if f.closed {
		return nil, errors.New("connection closed")
	}
	return f.tools, nil
}
func (f *fakeConn) Close() error { f.closed = true; return nil }

func newFakeLauncher(fail map[string]bool) (Launcher, *map[string]int) {
	counts := map[string]int{}
	return func(_ context.Context, server string) (Conn, error) {
		counts[server]++
		if fail[server] {
			return nil, errors.New("boom")
		}
		return &fakeConn{name: server}, nil
	}, &counts
}

func TestEnsureStartsAllServers(t *testing.T) {
	l, counts := newFakeLauncher(nil)
	m := New(DefaultConfig(), l, nil)
	s, err := m.Ensure(context.Background(), 1)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(s.conns) != 2 {
		t.Fatalf("conns = %d, want 2 (browser + chrome-devtools)", len(s.conns))
	}
	if (*counts)["browser"] != 1 || (*counts)["chrome-devtools"] != 1 {
		t.Fatalf("launch counts = %v", *counts)
	}
	// Calling Ensure twice must not relaunch healthy servers.
	if _, err := m.Ensure(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if (*counts)["browser"] != 1 {
		t.Fatalf("healthy server was relaunched: %v", *counts)
	}
}

// TestMarkBrokenTriggersRebuild is the unit-level form of the acceptance test
// "kill the browser process → the next call rebuilds within 10s": a dead connection is
// detected, the server is dropped, and the next Ensure relaunches it immediately.
func TestMarkBrokenTriggersRebuild(t *testing.T) {
	l, counts := newFakeLauncher(nil)
	m := New(DefaultConfig(), l, nil)
	s, _ := m.Ensure(context.Background(), 7)

	m.MarkBroken(7, "browser", errors.New("process exited"))
	if s.conns["browser"] != nil {
		t.Fatal("broken connection must be dropped")
	}
	if got := m.Status(7).Servers["browser"]; got == "live" {
		t.Fatalf("status still says live: %q", got)
	}

	start := time.Now()
	if _, err := m.Ensure(context.Background(), 7); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("rebuild took %s, acceptance is <10s", elapsed)
	}
	if (*counts)["browser"] != 2 {
		t.Fatalf("browser launches = %d, want 2", (*counts)["browser"])
	}
	if got := m.Status(7).Servers["browser"]; got != "live" {
		t.Fatalf("status after rebuild = %q", got)
	}
}

// TestBrokenServerDoesNotKillTheOther: Playwright crashing must not take DevTools down.
func TestBrokenServerDoesNotKillTheOther(t *testing.T) {
	l, counts := newFakeLauncher(map[string]bool{"browser": true})
	m := New(DefaultConfig(), l, nil)
	if _, err := m.Ensure(context.Background(), 2); err == nil {
		t.Fatal("expected an error when one server cannot start")
	}
	st := m.Status(2)
	if st.Servers["chrome-devtools"] != "live" {
		t.Fatalf("devtools should still be live: %v", st.Servers)
	}
	if (*counts)["chrome-devtools"] != 1 {
		t.Fatalf("devtools launches = %d", (*counts)["chrome-devtools"])
	}
	// The failure is counted for the status panel.
	if st.StartFailures == 0 {
		t.Fatal("start failures not recorded")
	}
}

func TestTabsAreIsolatedAndCapped(t *testing.T) {
	l, _ := newFakeLauncher(nil)
	cfg := DefaultConfig()
	cfg.MaxTabs = 2
	m := New(cfg, l, nil)
	if _, err := m.Ensure(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	a, err := m.OpenTab(3, "http://t/login")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.OpenTab(3, "http://t/admin")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("tab ids must be distinct")
	}
	tabs := m.Status(3).Tabs
	if len(tabs) != 2 || tabs[0].URL != "http://t/login" || tabs[1].URL != "http://t/admin" {
		t.Fatalf("tab model lost isolation: %+v", tabs)
	}
	// Opening tab 2 must not overwrite tab 1's URL.
	if tabs[0].URL == tabs[1].URL {
		t.Fatal("opening a second tab overwrote the first")
	}
	if _, err := m.OpenTab(3, "http://t/third"); err == nil {
		t.Fatal("expected the tab cap to reject a third tab")
	}
	m.CloseTab(3, a.ID)
	if got := m.Status(3).Tabs; len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("after close: %+v", got)
	}
	// Closing an unknown id is idempotent.
	m.CloseTab(3, "nope")
}

func TestGCClosesIdleSessions(t *testing.T) {
	l, _ := newFakeLauncher(nil)
	cfg := DefaultConfig()
	cfg.IdleTimeout = time.Minute
	m := New(cfg, l, nil)
	s, _ := m.Ensure(context.Background(), 4)
	if removed := m.GC(time.Now()); removed != 0 {
		t.Fatalf("fresh session collected: %d", removed)
	}
	if removed := m.GC(time.Now().Add(2 * time.Minute)); removed != 1 {
		t.Fatalf("idle session not collected: %d", removed)
	}
	if !s.conns["browser"].(*fakeConn).closed {
		t.Fatal("collected session left its connection open")
	}
	if m.Status(4).Alive {
		t.Fatal("session still reported alive after GC")
	}
}

func TestReleaseClosesEverything(t *testing.T) {
	l, _ := newFakeLauncher(nil)
	m := New(DefaultConfig(), l, nil)
	s, _ := m.Ensure(context.Background(), 5)
	m.Release(5)
	for name, c := range s.conns {
		if !c.(*fakeConn).closed {
			t.Fatalf("%s left open after Release", name)
		}
	}
	if m.Status(5).Alive {
		t.Fatal("released session still alive")
	}
}

// ── thumbnail store ──────────────────────────────────────────────────────

func TestThumbStoreDedupesAndServes(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewThumbStore(dir, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\nfake")
	h1, err := ts.Save(png)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := ts.Save(png) // identical bytes → same hash, one file
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash not content addressed: %s vs %s", h1, h2)
	}
	if got := ts.Count(); got != 1 {
		t.Fatalf("files = %d, want 1 after dedupe", got)
	}
	back, err := ts.Get(h1)
	if err != nil || string(back) != string(png) {
		t.Fatalf("get: %v / %q", err, back)
	}
	// Hash validation: traversal must be impossible.
	if _, err := ts.Get("../../etc/passwd"); err == nil {
		t.Fatal("bad hash accepted")
	}
	_ = os.Remove(filepath.Join(dir, "nonexistent"))
}

func TestThumbStoreTTLAndCap(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewThumbStore(dir, 100*time.Millisecond, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := ts.Save([]byte{byte('a' + i)}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	// Cap: only the 2 newest survive.
	if removed := ts.GC(time.Now()); removed != 2 {
		t.Fatalf("cap removed %d, want 2", removed)
	}
	if ts.Count() != 2 {
		t.Fatalf("count = %d, want 2", ts.Count())
	}
	// TTL: everything expires.
	time.Sleep(150 * time.Millisecond)
	if removed := ts.GC(time.Now()); removed != 2 {
		t.Fatalf("ttl removed %d, want 2", removed)
	}
	if ts.Count() != 0 {
		t.Fatalf("count = %d, want 0 after ttl", ts.Count())
	}
}
