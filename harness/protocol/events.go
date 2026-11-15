// Package protocol — typed event reasons for kernel decisions (Sprint 1.2/1.3).
//
// These are payload shapes for protocol Envelope items / activity rows. They are
// deliberately plain structs with stable JSON names: replay, gate and the console
// all read the same shapes, so a decision made in the kernel must be explainable
// without re-running it.
package protocol

// PermissionDecisionItem is the payload of a "permission_decision" item. It
// records the POLICY outcome (harness/permission), not the veto outcome: the
// platform hooks remain the only veto point and already emit their own
// intercept_request / denied activities.
type PermissionDecisionItem struct {
	Tool     string `json:"tool"`
	Behavior string `json:"behavior"` // allow | deny | ask
	Reason   string `json:"reason"`   // allow_rule | deny_rule | ask_rule | mode | no_rule | session_grant | read_only
	Rule     string `json:"rule,omitempty"`
	Mode     string `json:"mode"`
}

// RecoveryItem is the payload of a "recovery" item: the loop recovered from a
// transient failure instead of silently dropping the turn.
type RecoveryItem struct {
	Kind    string `json:"kind"`    // max_output_tokens_escalate | max_output_tokens_resume | reactive_compact | streaming_fallback
	Attempt int    `json:"attempt"` // 1-based; 0 for one-shot recoveries
	Detail  string `json:"detail,omitempty"`
}

// RecoveryKind constants. They mirror the recovery paths in norma/harness/query.go
// so the event stream can name them without importing the SDK.
const (
	RecoveryMaxTokensEscalate = "max_output_tokens_escalate"
	RecoveryMaxTokensResume   = "max_output_tokens_resume"
	RecoveryReactiveCompact   = "reactive_compact"
	RecoveryStreamingFallback = "streaming_fallback"
)
