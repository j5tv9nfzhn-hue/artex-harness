package evidence

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestChainAppendAndVerify(t *testing.T) {
	c := &Chain{TaskID: 7}
	ts := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	c.AppendAt(AuditFindingReported, "reporter", "SQL 注入 #1", 11, []string{"traffic:abc"}, ts)
	c.AppendAt(AuditEvidenceBound, "reporter", "绑定 1 条流量", 11, []string{"traffic:abc"}, ts.Add(time.Second))
	c.AppendAt(AuditRetestVerdict, "retester", "复测 reproduced", 11, nil, ts.Add(2*time.Second))
	if got := c.Verify(); !got.Valid || got.Entries != 3 {
		t.Fatalf("verify should pass: %+v", got)
	}
	if c.Entries[0].Seq != 1 || c.Entries[0].PrevHash != "" {
		t.Fatalf("first entry: seq=%d prev=%q", c.Entries[0].Seq, c.Entries[0].PrevHash)
	}
	if c.Entries[1].PrevHash != c.Entries[0].Hash {
		t.Fatalf("second entry must link to first hash")
	}
	// JSONL round-trip preserves the chain and still verifies.
	raw, err := c.MarshalJSONL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseJSONL(7, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Verify(); !got.Valid || got.Entries != 3 {
		t.Fatalf("round-trip verify: %+v", got)
	}
	// Appending to the parsed chain continues seq/hash linkage.
	parsed.AppendAt(AuditFindingConfirmed, "human", "确认", 11, nil, ts.Add(3*time.Second))
	if got := parsed.Verify(); !got.Valid || got.Entries != 4 {
		t.Fatalf("continued chain: %+v", got)
	}
}

func TestChainTamperDetected(t *testing.T) {
	c := &Chain{TaskID: 7}
	c.Append(AuditFindingReported, "reporter", "original", 11, nil)
	c.Append(AuditEvidenceBound, "reporter", "bound", 11, nil)
	// Tamper with the summary of entry 1: its hash no longer matches.
	tampered := *c
	tampered.Entries = append([]AuditEntry(nil), c.Entries...)
	tampered.Entries[0].Summary = "edited"
	if got := tampered.Verify(); got.Valid || got.BadSeq != 1 {
		t.Fatalf("content tamper should fail at seq 1: %+v", got)
	}
	// Delete entry 1: entry 2's prev_hash no longer links.
	deleted := &Chain{TaskID: 7, Entries: []AuditEntry{c.Entries[1]}}
	if got := deleted.Verify(); got.Valid {
		t.Fatalf("deletion should fail verification: %+v", got)
	}
	// Recompute the hash after tampering (attacker rewrites one entry):
	// the next entry's prev_hash still exposes it.
	forged := *c
	forged.Entries = append([]AuditEntry(nil), c.Entries...)
	forged.Entries[0].Summary = "edited"
	forged.Entries[0].Hash = ComputeHash(forged.Entries[0])
	if got := forged.Verify(); got.Valid || got.BadSeq != 2 {
		t.Fatalf("forged entry should fail at seq 2 via prev_hash: %+v", got)
	}
}

func TestChainEmptyVerifies(t *testing.T) {
	c := &Chain{TaskID: 7}
	if got := c.Verify(); !got.Valid || got.Entries != 0 {
		t.Fatalf("empty chain should verify: %+v", got)
	}
	if _, err := ParseJSONL(7, []byte("")); err != nil {
		t.Fatalf("empty jsonl: %v", err)
	}
	if _, err := ParseJSONL(7, []byte("{not json}\n")); err == nil {
		t.Fatalf("malformed line must fail")
	}
}

func TestAuditFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := AppendToFile(dir, 42, AuditFindingReported, "reporter", "first", 1, []string{"e1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendToFile(dir, 42, AuditFindingConfirmed, "human", "confirmed", 1, nil); err != nil {
		t.Fatal(err)
	}
	res, err := VerifyFile(dir, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.Entries != 2 || res.HeadHash == "" {
		t.Fatalf("file chain: %+v", res)
	}
	// A missing task is an empty, valid chain — not an error.
	res, err = VerifyFile(dir, 99)
	if err != nil || !res.Valid || res.Entries != 0 {
		t.Fatalf("missing file: res=%+v err=%v", res, err)
	}
	// Corrupt the file on disk: loading must fail loudly, not start over.
	path := AuditPath(dir, 42)
	raw, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte(strings.Replace(string(raw), "first", "forged", 1)), 0o600)
	if _, err := LoadFile(dir, 42); err == nil {
		// Load succeeds (JSON still parses); verification must catch it.
		if res, err := VerifyFile(dir, 42); err != nil || res.Valid {
			t.Fatalf("tampered file must fail verification: res=%+v err=%v", res, err)
		}
	}
}
