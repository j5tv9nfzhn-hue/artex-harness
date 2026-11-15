package server

import "testing"

// 目录匹配是"填空"而不是"覆盖"：前缀最长者优先，未知一律返回 ok=false，
// 好让界面如实显示"未知"而不是塞一个假数字。
func TestModelCatalogLongestPrefixWins(t *testing.T) {
	cases := []struct {
		model     string
		wantKnown bool
		wantCtx   int
		wantMatch string
	}{
		{"gpt-4o-mini", true, 128, "gpt-4o-mini"},
		{"gpt-4o", true, 128, "gpt-4o"},
		{"gpt-4.1-mini", true, 1047, "gpt-4.1-mini"},
		{"gpt-4.1", true, 1047, "gpt-4.1"},
		// 带厂商前缀的写法按最后一段匹配。
		{"openai/gpt-4o-mini", true, 128, "gpt-4o-mini"},
		{"MiniMax-M3.1-Flash", false, 0, ""},
		{"", false, 0, ""},
		{"some-unknown-model", false, 0, ""},
	}
	for _, c := range cases {
		e, matched, ok := lookupModelCatalog(c.model)
		if ok != c.wantKnown {
			t.Fatalf("%q: known=%v want %v", c.model, ok, c.wantKnown)
		}
		if !ok {
			continue
		}
		if e.ContextWindowK != c.wantCtx || matched != c.wantMatch {
			t.Fatalf("%q: ctx=%d match=%q, want ctx=%d match=%q", c.model, e.ContextWindowK, matched, c.wantCtx, c.wantMatch)
		}
	}
}

// 推理模型必须同时给出思考开关与可用的强度档位，否则界面只能让用户盲填。
func TestModelCatalogReasoningFamily(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-20250514", "o3", "deepseek-reasoner", "gpt-5"} {
		s := catalogSuggestion(model)
		if s["known"] != true {
			t.Fatalf("%q: expected a catalog hit", model)
		}
		if s["thinking_type"] != "enabled" {
			t.Fatalf("%q: thinking_type=%v, want enabled", model, s["thinking_type"])
		}
	}
}

// 限速不来自目录：它取决于账户档位，写死就是编造。建议值里不应出现这两个键。
func TestModelCatalogDoesNotInventRateLimits(t *testing.T) {
	s := catalogSuggestion("claude-sonnet-4-20250514")
	if _, ok := s["rate_per_second"]; ok {
		t.Fatal("catalog must not suggest rate_per_second")
	}
	if _, ok := s["rate_per_minute"]; ok {
		t.Fatal("catalog must not suggest rate_per_minute")
	}
}
