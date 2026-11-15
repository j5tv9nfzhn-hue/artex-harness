package server

import (
	"encoding/json"
	"testing"
)

// parseAgentSummary must understand every shape that has ever hit the activity
// table, including the Sprint 3 background prefix and the inline (#id) marker.
func TestParseAgentSummaryShapes(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantType   string
		wantPrompt string
	}{
		{"legacy foreground", "派发子代理 recon：列出端口", "recon", "列出端口"},
		{"background with id", "后台派发子代理 verify (#d2)：复现 SQLi", "verify", "复现 SQLi"},
		{"foreground with id", "派发子代理 recon (#d1)：探测入口", "recon", "探测入口"},
		{"raw tool_use", `Agent {"agent_type":"recon","prompt":"x"}`, "recon", "x"},
		{"no prompt", "派发子代理 recon", "recon", ""},
		{"not a delegation", "Bash ls", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at, p := parseAgentSummary(c.in)
			if at != c.wantType || p != c.wantPrompt {
				t.Fatalf("parseAgentSummary(%q) = (%q,%q), want (%q,%q)", c.in, at, p, c.wantType, c.wantPrompt)
			}
		})
	}
}

func TestParseDelegationMeta(t *testing.T) {
	got, bg := parseDelegationMeta(json.RawMessage(`{"delegation_id":"d7","background":true}`))
	if got != "d7" || !bg {
		t.Fatalf("meta parse = (%q,%v)", got, bg)
	}
	if got, bg := parseDelegationMeta(nil); got != "" || bg {
		t.Fatal("empty metadata must yield zero values")
	}
	if got, _ := parseDelegationMeta(json.RawMessage(`not json`)); got != "" {
		t.Fatal("malformed metadata must be ignored, not crash the panel")
	}
}
