package verify

import (
	"strings"
	"testing"
)

func good() Input {
	return Input{Severity: "high", EvidenceBindings: 2, HasRepro: true, Retest: RetestReproduced}
}

func TestCheckHappyPath(t *testing.T) {
	if d := Check(good()); !d.Allow || d.Overridden {
		t.Fatalf("happy path should allow without override: %+v", d)
	}
}

func TestCheckBlocksEachMissingPiece(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"no evidence", Input{Severity: "high", HasRepro: true, Retest: RetestReproduced}, "无流量证据"},
		{"no repro", Input{Severity: "high", EvidenceBindings: 1, Retest: RetestReproduced}, "无复现步骤"},
		{"no retest", Input{Severity: "high", EvidenceBindings: 1, HasRepro: true}, "未独立复测"},
		{"inconclusive", Input{Severity: "high", EvidenceBindings: 1, HasRepro: true, Retest: RetestInconclusive}, "inconclusive"},
		{"fixed", Input{Severity: "high", EvidenceBindings: 1, HasRepro: true, Retest: RetestFixed}, "fixed"},
		{"bad severity", Input{Severity: "urgent", EvidenceBindings: 1, HasRepro: true, Retest: RetestReproduced}, "严重等级无效"},
	}
	for _, tc := range cases {
		d := Check(tc.in)
		if d.Allow {
			t.Errorf("%s: should block", tc.name)
			continue
		}
		if joined := strings.Join(d.Reasons, ";"); !strings.Contains(joined, tc.want) {
			t.Errorf("%s: reasons %q should contain %q", tc.name, joined, tc.want)
		}
	}
	// Multiple failures are all reported, not just the first.
	d := Check(Input{Severity: "high"})
	if len(d.Reasons) != 3 {
		t.Errorf("all three missing pieces should be listed, got %v", d.Reasons)
	}
}

func TestCheckHumanOverride(t *testing.T) {
	in := good()
	in.Retest = RetestInconclusive
	in.HumanOverride = "目标已下线，无法复测；人工核对日志后确认"
	d := Check(in)
	if !d.Allow || !d.Overridden {
		t.Fatalf("override with reason should allow, flagged: %+v", d)
	}
	if !strings.Contains(d.Reasons[0], "人工越过验证门") {
		t.Fatalf("override reason should be first: %v", d.Reasons)
	}
	// Override without a reason is not an override.
	in.HumanOverride = "   "
	if d := Check(in); d.Allow {
		t.Fatalf("empty override reason must not allow: %+v", d)
	}
}

func TestRiskScoreAndRank(t *testing.T) {
	lo, hi, ok := RiskScore("critical")
	if !ok || lo != 9.0 || hi != 10.0 {
		t.Fatalf("critical band: %v-%v ok=%v", lo, hi, ok)
	}
	if _, _, ok := RiskScore("urgent"); ok {
		t.Fatalf("unknown severity must not score")
	}
	if RiskLabel("high") != "高危" || RiskLabel("x") != "未评级" {
		t.Fatalf("labels wrong")
	}
	if !(Rank("critical") < Rank("high") && Rank("high") < Rank("medium") && Rank("medium") < Rank("low")) {
		t.Fatalf("rank order wrong")
	}
}

func TestAllowTransition(t *testing.T) {
	if ok, _ := AllowTransition("pending", "false_positive", Decision{}); !ok {
		t.Fatalf("pending->false_positive must be allowed")
	}
	if ok, _ := AllowTransition("confirmed", "fixed", Decision{}); !ok {
		t.Fatalf("confirmed->fixed must be allowed")
	}
	if ok, msg := AllowTransition("pending", "confirmed", Check(Input{})); ok || !strings.Contains(msg, "验证门") {
		t.Fatalf("un-gated confirm must be blocked: ok=%v msg=%q", ok, msg)
	}
	if ok, _ := AllowTransition("pending", "confirmed", Check(good())); !ok {
		t.Fatalf("gated confirm should be allowed")
	}
}
