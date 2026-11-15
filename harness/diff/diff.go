// Package diff renders a unified diff between two text blobs.
//
// Why not `git diff`: HARNESS runs on bare-metal lab machines where the task
// workspace is usually NOT a git repository, and requiring git would make the
// review panel unusable exactly where it matters most. Instead the runtime keeps a
// baseline copy of the task workspace (taken when the task starts) and diffs the
// live tree against it — no VCS, no index, no hooks (docs/07 §P1).
//
// The implementation is a plain LCS over lines with a hard size guard: a diff must
// never be the thing that stalls a control plane.
package diff

import (
	"fmt"
	"strings"
)

// Status is the per-file outcome.
const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
)

// FileDiff is one file's diff.
type FileDiff struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Body is the unified diff text (empty when the caller only wants stats).
	Body string `json:"body,omitempty"`
	// Truncated marks a file too large to diff line-by-line; Body then carries a
	// whole-file replacement hunk instead of a real diff.
	Truncated bool `json:"truncated,omitempty"`
}

// maxDiffLines bounds the LCS table (n*m). Above it we degrade to "replace whole
// file" rather than allocate a huge matrix on a control-plane request.
const maxDiffLines = 4000

// maxBodyBytes bounds the rendered body.
const maxBodyBytes = 256 << 10

// Unified diffs cur against base for one path. base==nil means "added",
// cur==nil means "deleted".
func Unified(path string, base, cur []byte) FileDiff {
	out := FileDiff{Path: path}
	switch {
	case base == nil && cur == nil:
		return out
	case base == nil:
		out.Status = StatusAdded
	case cur == nil:
		out.Status = StatusDeleted
	default:
		if string(base) == string(cur) {
			out.Status = StatusModified // no change; caller filters on stats
			return out
		}
		out.Status = StatusModified
	}

	oldLines := splitLines(base)
	newLines := splitLines(cur)

	var body strings.Builder
	fmt.Fprintf(&body, "--- a/%s\n+++ b/%s\n", path, path)

	if len(oldLines) > maxDiffLines || len(newLines) > maxDiffLines {
		out.Truncated = true
		fmt.Fprintf(&body, "@@ whole file (too large to diff: %d → %d lines) @@\n", len(oldLines), len(newLines))
		for _, l := range oldLines {
			fmt.Fprintf(&body, "-%s\n", l)
			out.Deletions++
		}
		for _, l := range newLines {
			fmt.Fprintf(&body, "+%s\n", l)
			out.Additions++
		}
		out.Body = clipBody(body.String())
		return out
	}

	ops := editScript(oldLines, newLines)
	// Emit one hunk per contiguous run of changes, with 3 lines of context.
	const ctx = 3
	i := 0
	for i < len(ops) {
		if ops[i].op == opEqual {
			i++
			continue
		}
		start := i - ctx
		if start < 0 {
			start = 0
		}
		end := i
		for end < len(ops) && ops[end].op != opEqual {
			end++
		}
		tailEnd := end + ctx
		if tailEnd > len(ops) {
			tailEnd = len(ops)
		}
		oldStart, newStart := ops[start].oldLine, ops[start].newLine
		fmt.Fprintf(&body, "@@ -%d,%d +%d,%d @@\n", oldStart, countOld(ops[start:tailEnd]), newStart, countNew(ops[start:tailEnd]))
		for _, o := range ops[start:tailEnd] {
			switch o.op {
			case opEqual:
				fmt.Fprintf(&body, " %s\n", o.text)
			case opDelete:
				fmt.Fprintf(&body, "-%s\n", o.text)
				out.Deletions++
			case opInsert:
				fmt.Fprintf(&body, "+%s\n", o.text)
				out.Additions++
			}
		}
		i = tailEnd
		for i < len(ops) && ops[i].op == opEqual {
			// keep scanning; the outer loop skips context lines
			break
		}
	}
	out.Body = clipBody(body.String())
	return out
}

func clipBody(s string) string {
	if len(s) <= maxBodyBytes {
		return s
	}
	cut := maxBodyBytes
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "\n… (diff truncated)\n"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type editOp struct {
	op      opKind
	text    string
	oldLine int
	newLine int
}

// editScript builds a line-level edit script with a simple LCS DP.
func editScript(a, b []string) []editOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	ops := make([]editOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, editOp{op: opEqual, text: a[i], oldLine: i + 1, newLine: j + 1})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, editOp{op: opDelete, text: a[i], oldLine: i + 1, newLine: j + 1})
			i++
		default:
			ops = append(ops, editOp{op: opInsert, text: b[j], oldLine: i + 1, newLine: j + 1})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, editOp{op: opDelete, text: a[i], oldLine: i + 1, newLine: j + 1})
	}
	for ; j < m; j++ {
		ops = append(ops, editOp{op: opInsert, text: b[j], oldLine: i + 1, newLine: j + 1})
	}
	return ops
}

func countOld(ops []editOp) int {
	n := 0
	for _, o := range ops {
		if o.op != opInsert {
			n++
		}
	}
	return n
}

func countNew(ops []editOp) int {
	n := 0
	for _, o := range ops {
		if o.op != opDelete {
			n++
		}
	}
	return n
}
