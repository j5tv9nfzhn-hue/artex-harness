package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func TestCaptureListAndRestore(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "a.txt", "v1")
	write(t, ws, "sub/b.txt", "keep")
	cpDir := filepath.Join(t.TempDir(), "checkpoints")
	s := New(ws, cpDir)

	cp, err := s.Capture(1, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.Files) != 2 || cp.Revision != 1 {
		t.Fatalf("capture = %+v", cp)
	}

	// Mutate the workspace: edit one file, delete one, add one.
	write(t, ws, "a.txt", "v2-edited")
	if err := os.Remove(filepath.Join(ws, "sub/b.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, ws, "new.txt", "untracked-by-cp1")
	write(t, ws, "large.bin", strings.Repeat("x", MaxFileBytes+1))

	cps, err := s.List()
	if err != nil || len(cps) != 1 {
		t.Fatalf("List = %v, %v", cps, err)
	}
	plan, err := s.Preview(1)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(plan.Deleted, "new.txt") {
		t.Fatalf("plan should delete live-only file: %+v", plan)
	}
	if !contains(plan.Added, "sub/b.txt") {
		t.Fatalf("plan should recreate deleted checkpoint file: %+v", plan)
	}
	if plan.Note == "" || !strings.Contains(plan.Note, "Shell") {
		t.Fatal("the plan must state the shell/large-file limitation")
	}
	if !containsName(plan.Untracked, "large.bin") {
		t.Fatalf("plan must mark over-cap live files as untracked: %+v", plan.Untracked)
	}

	if _, err := s.Restore(1); err != nil {
		t.Fatal(err)
	}
	if got := read(t, ws, "a.txt"); got != "v1" {
		t.Fatalf("a.txt = %q, want restored v1", got)
	}
	if got := read(t, ws, "sub/b.txt"); got != "keep" {
		t.Fatalf("b.txt = %q, want recreated", got)
	}
	if got := read(t, ws, "new.txt"); got != "<missing>" {
		t.Fatalf("new.txt = %q, want removed", got)
	}
}

func TestRestoreRejectsTraversal(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(ws, filepath.Join(t.TempDir(), "cp"))
	if p := s.safePath("../outside.txt"); p != "" {
		t.Fatalf("traversal must be rejected, got %q", p)
	}
	if p := s.safePath("/etc/passwd"); p != "" {
		t.Fatalf("absolute path must be rejected, got %q", p)
	}
	if p := s.safePath("ok/../a.txt"); p != "" {
		t.Fatalf("embedded traversal must be rejected, got %q", p)
	}
	if p := s.safePath("ok/a.txt"); p == "" {
		t.Fatal("a normal relative path must be accepted")
	}
}

func TestCaptureSkipsHarnessBookkeeping(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, ".harness-baseline/base.txt", "base")
	write(t, ws, ".harness-checkpoints/revs/1/manifest.json", "{}")
	write(t, ws, "real.txt", "yes")
	s := New(ws, filepath.Join(t.TempDir(), "cp"))
	cp, err := s.Capture(1, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.Files) != 1 || cp.Files[0].Path != "real.txt" {
		t.Fatalf("bookkeeping leaked into the snapshot: %+v", cp.Files)
	}
}

func TestBlobsAreContentAddressed(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "a.txt", "same")
	write(t, ws, "b.txt", "same")
	s := New(ws, filepath.Join(t.TempDir(), "cp"))
	cp, err := s.Capture(1, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if cp.Files[0].Hash != cp.Files[1].Hash {
		t.Fatal("identical content must share a hash")
	}
	n := 0
	_ = filepath.WalkDir(filepath.Join(s.Dir(), blobsDir), func(_ string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if n != 1 {
		t.Fatalf("blob files = %d, want 1 (content-addressed dedup)", n)
	}
}

func TestLoadMissingAndBadRevision(t *testing.T) {
	s := New(t.TempDir(), filepath.Join(t.TempDir(), "cp"))
	if _, err := s.Capture(0, time.Time{}); err == nil {
		t.Fatal("revision 0 must be rejected")
	}
	if _, err := s.Load(999); err == nil {
		t.Fatal("missing checkpoint must return an error")
	}
	if cps, err := s.List(); err != nil || len(cps) != 0 {
		t.Fatalf("empty List = %v, %v", cps, err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want || strings.HasPrefix(s, want+"（") {
			return true
		}
	}
	return false
}
