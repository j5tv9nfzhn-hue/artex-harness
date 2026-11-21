package agent

import (
	"testing"
	"time"
)

// SetRunBudget 让任务建时传的 run_budget_minutes 生效：有值用任务级，
// 0 退回全局 runBudget()（HARNESS_RUN_BUDGET 或 20 分钟默认）。
func TestHarnessAgentRunBudgetOverride(t *testing.T) {
	h := NewHarnessAgent(nil, "m", t.TempDir(), nil, 0, 0)
	def := runBudget()
	if def <= 0 {
		t.Fatalf("global runBudget must be positive, got %v", def)
	}
	if got := h.RunBudget(); got != def {
		t.Fatalf("unset override should use global budget: got %v want %v", got, def)
	}
	h.SetRunBudget(90 * time.Minute)
	if got := h.RunBudget(); got != 90*time.Minute {
		t.Fatalf("override 90m: got %v", got)
	}
	h.SetRunBudget(0)
	if got := h.RunBudget(); got != def {
		t.Fatalf("clearing override should restore global budget: got %v want %v", got, def)
	}
}
