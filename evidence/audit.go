// Audit chain (stage 2, P1): an append-only, hash-chained log of
// evidence-relevant events, one audit.jsonl file per task.
//
// Why a separate chain when findings already live in PostgreSQL and
// evidence blobs are content-addressed? Because the database is mutable:
// a finding's status, report text or bindings can be edited or deleted,
// and db/evidence_integrity.go is explicitly a report, not a gate. The
// chain answers a different question — "was this history tampered with?" —
// by linking every entry to its predecessor:
//
//	hash[i] = SHA256(prev_hash || canonical(entry without hash))
//
// Change any field of any entry and its hash no longer matches; change
// the hash and the next entry's prev_hash no longer matches. Verification
// is pure (no DB, no network) so a report export can carry the chain
// verification result with it.
//
// This is tamper-evidence, not tamper-proofing: anyone with write access
// to the file can rewrite the whole chain from scratch. That is accepted
// and documented in docs/stages/S6-P1.md — the chain catches silent edits
// and partial rewrites, it does not replace file permissions or backups.
package evidence

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AuditKind is the event type recorded in the chain.
type AuditKind string

const (
	// AuditFindingReported records a finding being registered.
	AuditFindingReported AuditKind = "finding_reported"
	// AuditEvidenceBound records traffic evidence being bound to a finding.
	AuditEvidenceBound AuditKind = "evidence_bound"
	// AuditRetestVerdict records an independent retest verdict.
	AuditRetestVerdict AuditKind = "retest_verdict"
	// AuditFindingConfirmed records a finding passing the verification gate.
	AuditFindingConfirmed AuditKind = "finding_confirmed"
	// AuditFindingRejected records a finding being rejected / marked false positive.
	AuditFindingRejected AuditKind = "finding_rejected"
	// AuditHumanOverride records a human overriding the verification gate.
	// The reason is part of the entry, so an override is never silent.
	AuditHumanOverride AuditKind = "human_override"
	// AuditReportGenerated records a penetration report being generated.
	AuditReportGenerated AuditKind = "report_generated"
	// AuditTrafficReplay records a traffic exchange being replayed.
	AuditTrafficReplay AuditKind = "traffic_replay"
)

// AuditEntry is one line of audit.jsonl. Hash and PrevHash are hex SHA-256.
type AuditEntry struct {
	Seq       int64     `json:"seq"`
	TS        time.Time `json:"ts"`
	TaskID    int64     `json:"task_id"`
	Kind      AuditKind `json:"kind"`
	Actor     string    `json:"actor"`
	FindingID int64     `json:"finding_id,omitempty"`
	Summary   string    `json:"summary"`
	Evidence  []string  `json:"evidence,omitempty"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
}

// hashPayload is the canonical form hashed for an entry: every field
// except Hash itself, in a fixed JSON key order (encoding/json marshals
// struct fields in declaration order, so this struct IS the canonical form).
type hashPayload struct {
	Seq       int64     `json:"seq"`
	TS        time.Time `json:"ts"`
	TaskID    int64     `json:"task_id"`
	Kind      AuditKind `json:"kind"`
	Actor     string    `json:"actor"`
	FindingID int64     `json:"finding_id"`
	Summary   string    `json:"summary"`
	Evidence  []string  `json:"evidence"`
	PrevHash  string    `json:"prev_hash"`
}

// ComputeHash returns the chain hash for e (which must already carry Seq
// and PrevHash). It does not mutate e.
func ComputeHash(e AuditEntry) string {
	p := hashPayload{Seq: e.Seq, TS: e.TS, TaskID: e.TaskID, Kind: e.Kind,
		Actor: e.Actor, FindingID: e.FindingID, Summary: e.Summary,
		Evidence: e.Evidence, PrevHash: e.PrevHash}
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Chain is an in-memory audit chain for one task.
type Chain struct {
	TaskID  int64
	Entries []AuditEntry
}

// Append adds an entry, assigning Seq, PrevHash and Hash. TS defaults to
// time.Now (UTC) when zero, so callers in tests can pin it for determinism.
func (c *Chain) Append(kind AuditKind, actor, summary string, findingID int64, evidence []string) AuditEntry {
	return c.AppendAt(kind, actor, summary, findingID, evidence, time.Now().UTC())
}

// AppendAt is Append with an explicit timestamp (tests / imports).
func (c *Chain) AppendAt(kind AuditKind, actor, summary string, findingID int64, evidence []string, ts time.Time) AuditEntry {
	prev := ""
	if len(c.Entries) > 0 {
		prev = c.Entries[len(c.Entries)-1].Hash
	}
	e := AuditEntry{
		Seq: int64(len(c.Entries)) + 1, TS: ts, TaskID: c.TaskID,
		Kind: kind, Actor: actor, FindingID: findingID,
		Summary: summary, Evidence: append([]string(nil), evidence...),
		PrevHash: prev,
	}
	e.Hash = ComputeHash(e)
	c.Entries = append(c.Entries, e)
	return e
}

// VerifyResult is the outcome of Verify.
type VerifyResult struct {
	Valid    bool   `json:"valid"`
	Entries  int    `json:"entries"`
	BadSeq   int64  `json:"bad_seq,omitempty"`
	Problem  string `json:"problem,omitempty"`
	HeadHash string `json:"head_hash,omitempty"`
}

// Verify recomputes every hash and checks seq continuity and prev linkage.
// It reports the first bad entry (BadSeq) instead of failing silently.
func (c *Chain) Verify() VerifyResult {
	res := VerifyResult{Valid: true, Entries: len(c.Entries)}
	prev := ""
	for i, e := range c.Entries {
		wantSeq := int64(i) + 1
		if e.Seq != wantSeq {
			return VerifyResult{Valid: false, Entries: len(c.Entries), BadSeq: e.Seq,
				Problem: fmt.Sprintf("seq 不连续：期望 %d，实际 %d", wantSeq, e.Seq)}
		}
		if e.TaskID != c.TaskID {
			return VerifyResult{Valid: false, Entries: len(c.Entries), BadSeq: e.Seq,
				Problem: fmt.Sprintf("task_id 不一致：链属任务 %d，条目属 %d", c.TaskID, e.TaskID)}
		}
		if e.PrevHash != prev {
			return VerifyResult{Valid: false, Entries: len(c.Entries), BadSeq: e.Seq,
				Problem: "prev_hash 与上一条的 hash 不匹配（中间条目被改过或删过）"}
		}
		if got := ComputeHash(e); got != e.Hash {
			return VerifyResult{Valid: false, Entries: len(c.Entries), BadSeq: e.Seq,
				Problem: "hash 不匹配（条目内容被改过）"}
		}
		prev = e.Hash
	}
	if len(c.Entries) > 0 {
		res.HeadHash = c.Entries[len(c.Entries)-1].Hash
	}
	return res
}

// MarshalJSONL renders the chain as audit.jsonl (one JSON object per line).
func (c *Chain) MarshalJSONL() ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, e := range c.Entries {
		if err := enc.Encode(e); err != nil {
			return nil, err
		}
	}
	return []byte(b.String()), nil
}

// ParseJSONL parses audit.jsonl back into a chain. A malformed line fails
// the whole parse — a half-readable chain is not a chain.
func ParseJSONL(taskID int64, data []byte) (*Chain, error) {
	c := &Chain{TaskID: taskID}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, fmt.Errorf("audit.jsonl 第 %d 行解析失败: %w", line, err)
		}
		c.Entries = append(c.Entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

// AuditPath returns the audit.jsonl path for a task under dataDir.
func AuditPath(dataDir string, taskID int64) string {
	return filepath.Join(dataDir, "audit", fmt.Sprintf("%d.jsonl", taskID))
}

// AppendToFile appends one entry to the task's audit.jsonl, creating the
// file (and audit/ dir) on first use. It loads the existing chain first so
// Seq/PrevHash continue it; a corrupt existing file fails loudly instead
// of silently starting a new chain over the old one.
func AppendToFile(dataDir string, taskID int64, kind AuditKind, actor, summary string, findingID int64, evidence []string) (AuditEntry, error) {
	path := AuditPath(dataDir, taskID)
	chain, err := LoadFile(dataDir, taskID)
	if err != nil {
		return AuditEntry{}, err
	}
	e := chain.Append(kind, actor, summary, findingID, evidence)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return AuditEntry{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return AuditEntry{}, err
	}
	defer f.Close()
	raw, err := json.Marshal(e)
	if err != nil {
		return AuditEntry{}, err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return AuditEntry{}, err
	}
	return e, f.Sync()
}

// LoadFile loads a task's audit chain; a missing file is an empty chain,
// a corrupt file is an error.
func LoadFile(dataDir string, taskID int64) (*Chain, error) {
	data, err := os.ReadFile(AuditPath(dataDir, taskID))
	if os.IsNotExist(err) {
		return &Chain{TaskID: taskID}, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseJSONL(taskID, data)
}

// VerifyFile loads and verifies a task's audit chain in one call.
func VerifyFile(dataDir string, taskID int64) (VerifyResult, error) {
	chain, err := LoadFile(dataDir, taskID)
	if err != nil {
		return VerifyResult{}, err
	}
	return chain.Verify(), nil
}
