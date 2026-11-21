package db

import "testing"

// 建任务 run_budget_minutes 的合法范围：0=未设置用全局默认；
// 传值须在 5–480 分钟之间。
func TestValidateRunBudgetMinutes(t *testing.T) {
	for _, m := range []int{0, 5, 20, 480} {
		if err := ValidateRunBudgetMinutes(m); err != nil {
			t.Errorf("ValidateRunBudgetMinutes(%d) should pass, got %v", m, err)
		}
	}
	for _, m := range []int{-1, 1, 4, 481, 10000} {
		if err := ValidateRunBudgetMinutes(m); err == nil {
			t.Errorf("ValidateRunBudgetMinutes(%d) should fail", m)
		}
	}
}
