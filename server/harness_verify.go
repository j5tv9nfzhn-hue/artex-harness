// Verification gate + audit chain + pentest report endpoints (stage 2, P1).
//
// Wiring, not new rules: the rules live in harness/verify (pure) and
// evidence/audit (hash chain). This file only connects them to the
// existing data:
//   - PATCH /api/exploration/findings/{id} with status=confirmed now
//     runs the gate (see patchFinding in server.go). A blocked confirm
//     returns 422 with the failing checks; a human override
//     (human_override + reason in the PATCH body) is allowed and is
//     written to the audit chain as AuditHumanOverride.
//   - GET /api/harness/audit-chain?task= verifies the task's audit.jsonl.
//   - POST /api/traffic/replay replays one recorded exchange and reports
//     whether it reproduced the original.
//   - GET /api/report/pentest?task= renders the professional report
//     template (report.PentestMarkdown).
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/evidence"
	"github.com/Autumn-27/artex/harness/verify"
	"github.com/Autumn-27/artex/report"
	"github.com/Autumn-27/artex/traffic"
)

// firstNonEmpty returns the first non-blank string, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// findingVerifyGate builds the gate input for one finding from the
// database: traffic bindings count (GetFinding.TrafficCount), repro text
// (report or evidence), and the latest retest verdict.
func (s *Server) findingVerifyGate(f *db.DBFinding, humanOverride string) (verify.Decision, error) {
	in := verify.Input{
		Severity:         f.Severity,
		EvidenceBindings: f.TrafficCount,
		HasRepro:         strings.TrimSpace(f.Report) != "" || strings.TrimSpace(f.Evidence) != "",
		HumanOverride:    humanOverride,
	}
	retests, err := s.m.pg.ListFindingRetests(f.ID)
	if err != nil {
		return verify.Decision{}, err
	}
	for _, r := range retests {
		if r.Verdict != "" {
			in.Retest = verify.RetestVerdict(r.Verdict)
			break
		}
	}
	return verify.Check(in), nil
}

// auditAppend writes one entry to the task's audit.jsonl. It never fails
// a request: a corrupt chain is logged (and surfaced by the audit-chain
// endpoint), not silently ignored.
func (s *Server) auditAppend(taskID int64, kind evidence.AuditKind, actor, summary string, findingID int64, refs []string) {
	if taskID <= 0 {
		return
	}
	if _, err := evidence.AppendToFile(s.m.dir, taskID, kind, actor, summary, findingID, refs); err != nil {
		log.Printf("[audit] task=%d kind=%s: %v", taskID, kind, err)
	}
}

// harnessAuditChain serves GET /api/harness/audit-chain?task=<id>.
func (s *Server) harnessAuditChain(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	chain, err := evidence.LoadFile(s.m.dir, taskID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	res := chain.Verify()
	writeJSON(w, 200, map[string]any{
		"task_id": taskID, "valid": res.Valid, "entries": res.Entries,
		"head_hash": res.HeadHash, "problem": res.Problem, "chain": chain.Entries,
	})
}

// trafficReplay serves POST /api/traffic/replay {"exchange_id": "..."}.
// It replays the recorded request against the live target and reports
// whether the response reproduced the original exchange.
func (s *Server) trafficReplay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExchangeID string `json:"exchange_id"`
		TaskID     int64  `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ExchangeID) == "" {
		writeErr(w, 400, "exchange_id required")
		return
	}
	tr := s.m.Traffic()
	if tr == nil {
		writeErr(w, 503, "流量记录未启用")
		return
	}
	rawReq, rawResp, err := tr.Get(body.ExchangeID)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	// Original status comes from the recorded response head ("HTTP 200").
	origStatus := 0
	if first, _, ok := strings.Cut(strings.TrimSpace(rawResp), "\n"); ok {
		if fields := strings.Fields(first); len(fields) >= 2 {
			origStatus, _ = strconv.Atoi(fields[1])
		}
	}
	spec, err := traffic.ParseReplaySpec(body.ExchangeID, rawReq, origStatus)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	res, err := traffic.Replay(r.Context(), spec)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if body.TaskID > 0 {
		note := fmt.Sprintf("重放 %s：原始 %d，重放 %d，复现=%v", body.ExchangeID, origStatus, res.Status, res.Reproduced)
		s.auditAppend(body.TaskID, evidence.AuditTrafficReplay, "human", note, 0, []string{body.ExchangeID})
	}
	writeJSON(w, 200, map[string]any{
		"exchange_id": body.ExchangeID, "reproduced": res.Reproduced,
		"status": res.Status, "original_status": origStatus,
		"resp_len": res.RespLen, "note": res.Note, "curl": spec.CurlCommand(),
	})
}

// pentestReport serves GET /api/report/pentest?task=<id> as Markdown.
func (s *Server) pentestReport(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	fs, err := s.m.pg.ListFindingsForExport(db.FindingFilter{TaskID: t.ID}, nil)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var scope, outOfScope []string
	if as := s.m.Assets(); as != nil && taskID > 0 {
		if rows, err := as.ListTaskScopeWithSources(taskID); err == nil {
			for _, row := range rows {
				v := strings.TrimSpace(row.Domain)
				if v == "" {
					v = strings.TrimSpace(row.Net)
				}
				if v == "" {
					v = strings.TrimSpace(row.Value)
				}
				if v == "" {
					v = strings.TrimSpace(row.CompanyName)
				}
				if v == "" {
					v = row.Kind
				}
				scope = append(scope, fmt.Sprintf("%s：%s", row.Kind, v))
			}
		}
	}
	auditRes, _ := evidence.VerifyFile(s.m.dir, taskID)
	in := report.PentestInput{
		Title: t.Description, Goal: t.Goal, Scope: scope, OutOfScope: outOfScope,
		Version: "v1", GeneratedAt: time.Now(),
		AuditValid: auditRes.Valid, AuditEntries: auditRes.Entries,
		AuditHeadHash: auditRes.HeadHash, AuditProblem: auditRes.Problem,
	}
	for _, f := range fs {
		pf := report.PentestFinding{
			ID: f.ID, Title: firstNonEmpty(f.Name, f.VulnClass, "未分类"), VulnClass: f.VulnClass,
			Severity: f.Severity, Status: f.Status, Summary: f.Summary, TrafficCount: f.TrafficCount,
			CreatedAt: f.CreatedAt,
			// 修复建议只存在于发现的详细报告（report 字段的「修复建议」一节），
			// 没有独立列：从那里提取，提不到才算未提供。
			Remediation: report.ExtractRemediation(f.Report),
		}
		if ev := strings.TrimSpace(f.Evidence); ev != "" {
			pf.ReproSteps = []string{ev}
		}
		if strings.TrimSpace(f.Report) != "" {
			pf.ReproSteps = append(pf.ReproSteps, "详细报告见漏洞记录。")
		}
		// Verified only when the latest retest reproduced it; a confirmed
		// finding without that is shown as a human override, not verified.
		if retests, err := s.m.pg.ListFindingRetests(f.ID); err == nil {
			for _, rt := range retests {
				if rt.Verdict == "reproduced" {
					pf.Verified = true
					break
				}
				if rt.Verdict != "" {
					break
				}
			}
		}
		if f.Status == db.FindingConfirmed && !pf.Verified {
			pf.OverrideReason = "状态为已确认，但本次生成时未见 reproduced 复测结论"
		}
		in.Findings = append(in.Findings, pf)
	}
	md := report.PentestMarkdown(in)
	if taskID > 0 {
		s.auditAppend(taskID, evidence.AuditReportGenerated, "human",
			fmt.Sprintf("生成渗透报告 v1：%d 个发现，审计链 %d 条、验证=%v", len(in.Findings), auditRes.Entries, auditRes.Valid), 0, nil)
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(md))
}
