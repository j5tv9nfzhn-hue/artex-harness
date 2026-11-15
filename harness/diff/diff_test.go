package diff

import (
	"strings"
	"testing"
)

func TestUnifiedDetectsStatus(t *testing.T) {
	added := Unified("a.txt", nil, []byte("hello\n"))
	if added.Status != StatusAdded || added.Additions != 1 || added.Deletions != 0 {
		t.Fatalf("added = %+v", added)
	}
	deleted := Unified("a.txt", []byte("hello\n"), nil)
	if deleted.Status != StatusDeleted || deleted.Deletions != 1 {
		t.Fatalf("deleted = %+v", deleted)
	}
	same := Unified("a.txt", []byte("x\n"), []byte("x\n"))
	if same.Additions != 0 || same.Deletions != 0 {
		t.Fatalf("identical files must produce no changes: %+v", same)
	}
}

func TestUnifiedProducesRealDiff(t *testing.T) {
	base := []byte("line1\nline2\nline3\nline4\nline5\n")
	cur := []byte("line1\nline2 changed\nline3\nline4\nline6\n")
	d := Unified("f.txt", base, cur)
	if d.Status != StatusModified {
		t.Fatalf("status = %s", d.Status)
	}
	if !strings.Contains(d.Body, "-line2") || !strings.Contains(d.Body, "+line2 changed") {
		t.Fatalf("body missing the change:\n%s", d.Body)
	}
	if !strings.Contains(d.Body, "+line6") || !strings.Contains(d.Body, "-line5") {
		t.Fatalf("body missing the tail change:\n%s", d.Body)
	}
	// Context lines are prefixed with a space so the body is a valid unified diff.
	if !strings.Contains(d.Body, " line1") {
		t.Fatalf("missing context line:\n%s", d.Body)
	}
	if d.Additions == 0 || d.Deletions == 0 {
		t.Fatalf("stats = +%d -%d", d.Additions, d.Deletions)
	}
}

func TestUnifiedCountsMatchHunks(t *testing.T) {
	base := []byte("a\nb\nc\n")
	cur := []byte("a\nB\nc\n")
	d := Unified("x", base, cur)
	if d.Additions != 1 || d.Deletions != 1 {
		t.Fatalf("stats = +%d -%d, want +1 -1", d.Additions, d.Deletions)
	}
}

func TestUnifiedDegradesOnHugeFiles(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxDiffLines+10; i++ {
		b.WriteString("line\n")
	}
	d := Unified("big.txt", []byte(b.String()), []byte("small\n"))
	if !d.Truncated {
		t.Fatal("a file above the line cap must be marked truncated")
	}
	if !strings.Contains(d.Body, "whole file") {
		t.Fatalf("degraded body should say so:\n%.200s", d.Body)
	}
}

func TestUnifiedHandlesMissingTrailingNewline(t *testing.T) {
	d := Unified("n.txt", []byte("a\nb"), []byte("a\nb\nc"))
	if !strings.Contains(d.Body, "+c") {
		t.Fatalf("body = %s", d.Body)
	}
	if d.Additions != 1 {
		t.Fatalf("additions = %d", d.Additions)
	}
}
