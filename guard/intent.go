package guard

// Stage 1 (P0) — intent-level deterministic classification.
//
// Why this file exists: docs/02 §3.5 shows approval prompts are mostly
// theatre (93% approved) and only ~4% of natural-language safety rules map
// to an enforced control. The remaining small set of interceptions must
// therefore be deterministic and mechanical, not prompt-based and not
// deletable by removing a DB rule.
//
// This classifier is pure: no LLM, no network, no DB. It maps a tool call
// to an intent kind (filesystem_delete, network_outbound, lang_exec, …)
// because the same binary can be harmless or destructive depending on its
// arguments. The hard-deny set below is the safety floor: it cannot be
// disabled from the UI and it runs before the DB intercept rules.
//
// Risk levels: low / medium / high / critical. Only critical is a
// hard deny. High is surfaced in the audit log and left to the DB
// rules / LLM judge / human ask path to decide.

import (
	"encoding/json"
	"regexp"
	"strings"
)

// IntentKind is the intent a tool call maps to.
type IntentKind string

const (
	IntentBenign              IntentKind = "benign"
	IntentFilesystemDelete    IntentKind = "filesystem_delete"
	IntentFilesystemWrite     IntentKind = "filesystem_write"
	IntentDatabaseDestructive IntentKind = "database_destructive"
	IntentNetworkOutbound     IntentKind = "network_outbound"
	IntentLangExec            IntentKind = "lang_exec"
	IntentCredentialAccess    IntentKind = "credential_access"
	IntentPrivilegeEscalation IntentKind = "privilege_escalation"
	IntentExfiltration        IntentKind = "exfiltration"
	IntentUnknown             IntentKind = "unknown"
)

// Risk is low / medium / high / critical.
type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

// Intent is the deterministic classification of one tool call.
type Intent struct {
	Kind    IntentKind `json:"kind"`
	Risk    Risk       `json:"risk"`
	Reason  string     `json:"reason"`
	Matched string     `json:"matched,omitempty"`
}

// IsHardDeny reports whether this intent must be blocked mechanically,
// regardless of DB rules or judge verdict.
func (in Intent) IsHardDeny() bool {
	return in.Risk == RiskCritical
}

type intentPattern struct {
	kind   IntentKind
	risk   Risk
	reason string
	re     *regexp.Regexp
}

func mustPattern(kind IntentKind, risk Risk, reason, pat string) intentPattern {
	return intentPattern{kind: kind, risk: risk, reason: reason, re: regexp.MustCompile(pat)}
}

// Patterns are ordered: critical first, then high, then medium.
// Keep them narrow: a false positive here blocks a legitimate pentest
// step, a false negative here is caught by the DB rules / judge below.
var intentPatterns = []intentPattern{
	// --- critical: mechanical hard deny, see docs/02 §3.5 ---
	mustPattern(IntentFilesystemDelete, RiskCritical, "极端破坏：递归删除根或全盘", `(?i)(^|[;&|]\s*)rm\s+(-[^ ]*r[^ ]*\s+)+(-{0,2}no-preserve-root\s+)?(/\s*$|/\*\s*$|/\s+[;&|]|--no-preserve-root)`),
	mustPattern(IntentFilesystemDelete, RiskCritical, "极端破坏：格式化文件系统", `(?i)\bmkfs(\.\w+)?\s+`),
	mustPattern(IntentFilesystemDelete, RiskCritical, "极端破坏：dd 写入块设备", `(?i)\bdd\s+[^|]*\bof=/dev/`),
	mustPattern(IntentFilesystemDelete, RiskCritical, "极端破坏：shred 覆写删除", `(?i)\bshred\s+`),
	mustPattern(IntentDatabaseDestructive, RiskCritical, "极端破坏：DROP DATABASE/TABLE", `(?i)\bdrop\s+(database|table)\b`),
	mustPattern(IntentDatabaseDestructive, RiskCritical, "极端破坏：TRUNCATE 表", `(?i)\btruncate\s+(table\s+)?\w+`),

	// --- high: surfaced, left to DB rules / ask path ---
	mustPattern(IntentPrivilegeEscalation, RiskHigh, "提权：sudo/su/setuid", `(?i)(^|[;&|]\s*)(sudo\s+|su\s+(-|root\b)|chmod\s+.*[47][0-9]{3}\b|chmod\s+u\+s\b)`),
	mustPattern(IntentCredentialAccess, RiskHigh, "凭据访问：私钥/影子文件/云凭据", `(?i)(\.ssh/(id_rsa|id_ed25519|id_ecdsa)|/etc/shadow|/etc/gshadow|\.aws/credentials|~/.kube/config)`),
	mustPattern(IntentCredentialAccess, RiskHigh, "凭据访问：环境中读取 token/密钥", `(?i)\benv\b[^|]*\b(token|secret|api[_-]?key|password)\b`),
	mustPattern(IntentExfiltration, RiskHigh, "外传：scp/rsync/nc 回连/外发文件", `(?i)(^|[;&|]\s*)(scp\s+[^ ]+\s+[^ ]+@|rsync\s+[^ ]*\s+[^ ]+@|\bnc\s+[^ ]+\s+\d+\s*<|curl\s+[^|]*\s+-F\b|curl\s+[^|]*\s+--data-binary\s+@)`),
	mustPattern(IntentFilesystemDelete, RiskHigh, "删除：rm -rf 非根路径", `(?i)(^|[;&|]\s*)rm\s+(-[^ ]*r[^ ]*\s+)+`),
	mustPattern(IntentDatabaseDestructive, RiskHigh, "数据库写：DELETE FROM 无 WHERE 提示", `(?i)\bdelete\s+from\s+\w+\s*(;|$)`),

	// --- medium: network / code execution, needs scope check ---
	mustPattern(IntentNetworkOutbound, RiskMedium, "出站网络：curl/wget/ssh/nc", `(?i)(^|[;&|]\s*)(curl|wget|ssh|nc|ncat)\s+`),
	mustPattern(IntentLangExec, RiskMedium, "代码执行：python/node/bash -c", `(?i)(^|[;&|]\s*)(python3?\s+-c|node\s+-e|bash\s+-c|sh\s+-c|powershell\s+.*-enc)`),
}

// toolInputText extracts the human-readable text a classifier should look at:
// Bash/shell_open command, shell_send text, WebFetch/WebSearch url, Write/Edit
// file_path + content, otherwise the raw JSON.
func toolInputText(tool string, input []byte) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return string(input)
	}
	getStr := func(keys ...string) string {
		for _, k := range keys {
			if raw, ok := m[k]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					return s
				}
			}
		}
		return ""
	}
	switch tool {
	case "Bash", "shell_open":
		if s := getStr("command"); s != "" {
			return s
		}
	case "shell_send":
		if s := getStr("text"); s != "" {
			return s
		}
	case "WebFetch", "web_search", "WebSearch":
		if s := getStr("url", "query"); s != "" {
			return s
		}
	case "Write", "Edit", "MultiEdit":
		if s := getStr("file_path", "filePath", "path"); s != "" {
			return s
		}
	}
	// Fall back to the whole input so a pattern hidden in another field
	// is still seen. Keep it bounded for the audit log.
	s := string(input)
	if len(s) > 8192 {
		s = s[:8192]
	}
	return s
}

// ClassifyIntent maps a tool call to an intent, deterministically.
// It never calls an LLM and never touches the DB.
func ClassifyIntent(tool string, input []byte) Intent {
	text := toolInputText(tool, input)
	trimmed := strings.TrimSpace(text)

	// Write/Edit tools are filesystem writes by construction.
	switch tool {
	case "Write", "Edit", "MultiEdit":
		return Intent{Kind: IntentFilesystemWrite, Risk: RiskMedium, Reason: "文件写：Write/Edit 工具", Matched: trimmed}
	case "WebFetch":
		return Intent{Kind: IntentNetworkOutbound, Risk: RiskMedium, Reason: "出站网络：WebFetch", Matched: trimmed}
	case "web_search", "WebSearch":
		return Intent{Kind: IntentNetworkOutbound, Risk: RiskLow, Reason: "出站网络：web_search（只读搜索）", Matched: trimmed}
	}

	if trimmed == "" {
		return Intent{Kind: IntentBenign, Risk: RiskLow, Reason: "空输入"}
	}
	for _, p := range intentPatterns {
		if loc := p.re.FindString(text); loc != "" {
			return Intent{Kind: p.kind, Risk: p.risk, Reason: p.reason, Matched: loc}
		}
	}
	// Bash-like tools with no matched pattern are still code execution,
	// but low risk unless a pattern above fired.
	switch tool {
	case "Bash", "shell_open", "shell_send":
		return Intent{Kind: IntentLangExec, Risk: RiskLow, Reason: "代码执行：shell 调用，未命中高危模式", Matched: ""}
	}
	return Intent{Kind: IntentBenign, Risk: RiskLow, Reason: "未命中意图模式"}
}
