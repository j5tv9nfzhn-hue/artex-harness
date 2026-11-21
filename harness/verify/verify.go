// Package verify is the independent verification gate (stage 2, P1).
//
// Upstream's db/evidence_integrity.go is explicit that it is a REPORT,
// not a gate: a finding with no traffic evidence can still be marked
// confirmed, and a retest verdict of "inconclusive" changes nothing.
// This package turns that report into an enforced decision, in pure
// functions (no DB, no network) so the server handler and tests share
// exactly the same rule:
//
//	A finding may be marked confirmed only if ALL of:
//	  1. it has at least one bound traffic-evidence snapshot, and
//	  2. it has reproduction steps (non-empty report/evidence text), and
//	  3. the latest independent retest reproduced it (verdict "reproduced"),
//	     OR a human overrode the gate with an explicit, recorded reason.
//
// An override is not a bypass: it must carry a reason, it is recorded in
// the audit chain (evidence.AuditHumanOverride), and the report marks the
// finding as human-overridden so a reader can tell it apart from a
// retest-verified one.
//
// The gate only guards the transition TO confirmed. Moving a confirmed
// finding to fixed / resolved / false_positive / ignored is always
// allowed (those are human triage, not new claims), and moving a
// suspected finding to false_positive is the correct outcome when the
// retest did NOT reproduce it.
package verify

import (
	"fmt"
	"strings"
)

// RetestVerdict mirrors db FindingRetest verdicts.
type RetestVerdict string

const (
	RetestReproduced   RetestVerdict = "reproduced"
	RetestFixed        RetestVerdict = "fixed"
	RetestInconclusive RetestVerdict = "inconclusive"
	RetestNone         RetestVerdict = ""
)

// Input is everything the gate needs about one finding.
type Input struct {
	// Severity must be critical/high/medium/low (db.ValidSeverity).
	Severity string
	// EvidenceBindings is the count of bound traffic-evidence snapshots.
	EvidenceBindings int
	// HasRepro is true when the finding carries reproduction steps
	// (report or evidence text, non-empty after trimming).
	HasRepro bool
	// Retest is the latest independent retest verdict, "" when never retested.
	Retest RetestVerdict
	// HumanOverride, when set, must carry Reason. It records a human
	// decision to confirm despite a failed/missing retest.
	HumanOverride string
}

// Decision is the gate outcome. Reasons lists every failing check, in
// check order, so the API can return them verbatim.
type Decision struct {
	Allow   bool
	Reasons []string
	// Overridden is true when Allow rests on a human override.
	Overridden bool
}

// Check decides whether a finding may be marked confirmed.
func Check(in Input) Decision {
	var reasons []string
	switch strings.ToLower(strings.TrimSpace(in.Severity)) {
	case "critical", "high", "medium", "low":
	default:
		reasons = append(reasons, fmt.Sprintf("严重等级无效：%q（应为 critical/high/medium/low）", in.Severity))
	}
	if in.EvidenceBindings <= 0 {
		reasons = append(reasons, "无流量证据绑定：先用 bind_finding_traffic 绑定已核实的流量，再确认")
	}
	if !in.HasRepro {
		reasons = append(reasons, "无复现步骤：报告/证据为空，无法复核")
	}
	override := strings.TrimSpace(in.HumanOverride)
	if in.Retest != RetestReproduced {
		switch in.Retest {
		case RetestNone:
			reasons = append(reasons, "未独立复测：先发起复测并得到 reproduced 结论")
		case RetestFixed:
			reasons = append(reasons, "复测结论为 fixed：漏洞已修复，不应再标记为已确认")
		case RetestInconclusive:
			reasons = append(reasons, "复测结论为 inconclusive：未复现也未证伪，需人工判定")
		default:
			reasons = append(reasons, fmt.Sprintf("复测结论未知：%q", string(in.Retest)))
		}
	}
	if len(reasons) == 0 {
		return Decision{Allow: true}
	}
	if override == "" {
		return Decision{Allow: false, Reasons: reasons}
	}
	// Human override: allowed, but only with a reason, and flagged.
	return Decision{Allow: true, Overridden: true,
		Reasons: append([]string{"人工越过验证门：" + override}, reasons...)}
}

// RiskScore maps a severity to a CVSS-style numeric band. It is a
// coarse, documented mapping for report ordering — not a CVSS calculation:
// the harness does not score vectors, it only orders by declared severity.
func RiskScore(severity string) (low, high float64, ok bool) {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 9.0, 10.0, true
	case "high":
		return 7.0, 8.9, true
	case "medium":
		return 4.0, 6.9, true
	case "low":
		return 0.1, 3.9, true
	default:
		return 0, 0, false
	}
}

// RiskLabel is the Chinese label for a severity, for report tables.
func RiskLabel(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return "严重"
	case "high":
		return "高危"
	case "medium":
		return "中危"
	case "low":
		return "低危"
	default:
		return "未评级"
	}
}

// Rank orders severities for report sorting (lower = more severe).
func Rank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// AllowTransition reports whether a finding may move from one triage
// state to another. The only guarded transition is * -> confirmed,
// which must go through Check; every other transition is human triage
// and is allowed (including confirmed -> false_positive, which is how a
// retest that failed to reproduce is recorded).
func AllowTransition(from, to string, gate Decision) (bool, string) {
	from, to = strings.ToLower(strings.TrimSpace(from)), strings.ToLower(strings.TrimSpace(to))
	if from == to {
		return true, ""
	}
	if to == "confirmed" {
		if !gate.Allow {
			return false, "验证门未通过：" + strings.Join(gate.Reasons, "；")
		}
		return true, ""
	}
	// No other transition is gated.
	return true, ""
}
