package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingReader yields some bytes then errors, simulating a dropped upload.
type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("connection reset")
	}
	n := f.n
	if n > len(p) {
		n = len(p)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	f.n -= n
	return n, nil
}

// TestWriteFileAtomicKeepsOriginalOnFailure is the regression test for
// docs/upstream/SOURCE-REVIEW.md #5: os.Create truncated the destination before
// copying, so a failed upload destroyed the existing file and left a half-written
// one in its place.
func TestWriteFileAtomicKeepsOriginalOnFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "wordlist.txt")
	if err := os.WriteFile(dest, []byte("ORIGINAL CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(dest, &failingReader{n: 1000}); err == nil {
		t.Fatal("a failing reader must surface an error")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("original file disappeared: %v", err)
	}
	if string(got) != "ORIGINAL CONTENT" {
		t.Fatalf("original content was damaged: %q", got)
	}
	// No half-written temp file may be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestWriteFileAtomicReplacesCompletely: on success the destination holds exactly
// the new bytes, with predictable permissions.
func TestWriteFileAtomicReplacesCompletely(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(dest, strings.NewReader("new content")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new content" {
		t.Fatalf("content = %q", got)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("permissions = %v, want 0644", info.Mode().Perm())
	}
	// A single file remains: no stray temp files next to it.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one file, got %d", len(entries))
	}
}

// TestWriteFileAtomicCreatesNewFile covers the common case of a first upload.
func TestWriteFileAtomicCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "new.bin")
	if err := writeFileAtomic(dest, strings.NewReader("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "hello" {
		t.Fatalf("content = %q err=%v", got, err)
	}
}
