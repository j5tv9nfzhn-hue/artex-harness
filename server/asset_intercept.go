package server

import (
	"fmt"
	"net"
	"strings"
)

// --- asset intercept rule CRUD ---
//
// Global asset blocklist: exact/fuzzy domain·ip·url + CIDR. This layer only
// persists rules; the actual match/enforcement logic lives elsewhere.

// --- helpers ---

type assetInterceptRuleReq struct {
	Enabled bool   `json:"enabled"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	Note    string `json:"note"`
}

// validateAssetInterceptRuleReq normalizes and validates a rule. It trims the
// pattern and, for the strictly-formatted kinds (exact_ip / cidr), rejects
// malformed values; fuzzy kinds and domain/url patterns are left as free text
// (the enforcement layer decides how to interpret them).
func validateAssetInterceptRuleReq(req *assetInterceptRuleReq) error {
	req.Pattern = strings.TrimSpace(req.Pattern)
	if req.Pattern == "" {
		return fmt.Errorf("pattern 不能为空")
	}
	switch req.Kind {
	case "exact_domain", "exact_url", "fuzzy_domain", "fuzzy_ip", "fuzzy_url":
		// free-form, no format check
	case "exact_ip":
		if net.ParseIP(req.Pattern) == nil {
			return fmt.Errorf("exact_ip 不是有效 IP 地址：%s", req.Pattern)
		}
	case "cidr":
		if _, _, err := net.ParseCIDR(req.Pattern); err != nil {
			return fmt.Errorf("cidr 不是有效网段（形如 192.168.0.0/16）：%s", req.Pattern)
		}
	default:
		return fmt.Errorf("kind 无效：%s", req.Kind)
	}
	return nil
}
