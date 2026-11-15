package server

import (
	"sort"
	"strings"
)

// ModelCatalogEntry 是公开模型文档对某一个模型家族的说法：它接受多大的窗口、
// 推理开关怎么表达、认哪些强度档位。
//
// 它**只用来填空**：供应商自己的 /models 端点若报了容量，以端点为准；这里留零值
// 的字段含义是"这里也不知道，让用户自己填"，绝不用猜的数字覆盖。
//
// 刻意不含限速：速率取决于账户档位（Tier），不取决于模型。写一个数字就是编造。
type ModelCatalogEntry struct {
	Prefix           string   `json:"match"`
	Label            string   `json:"label,omitempty"`
	ContextWindowK   int      `json:"context_window_k,omitempty"`
	MaxOutputK       int      `json:"max_output_k,omitempty"`
	ThinkingType     string   `json:"thinking_type,omitempty"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
	DefaultEffort    string   `json:"default_effort,omitempty"`
	Vision           bool     `json:"vision,omitempty"`
}

// modelCatalogTable 按模型 id 前缀登记已知参数。匹配时长前缀优先，所以
// gpt-4o-mini 会先于 gpt-4o 命中。
var modelCatalogTable = []ModelCatalogEntry{
	// Anthropic
	{Prefix: "claude-opus-4", Label: "Claude Opus 4", ContextWindowK: 200, MaxOutputK: 32,
		ThinkingType: "enabled", ReasoningEfforts: []string{"low", "medium", "high", "max"}, DefaultEffort: "medium", Vision: true},
	{Prefix: "claude-sonnet-4", Label: "Claude Sonnet 4", ContextWindowK: 200, MaxOutputK: 64,
		ThinkingType: "enabled", ReasoningEfforts: []string{"low", "medium", "high", "max"}, DefaultEffort: "medium", Vision: true},
	{Prefix: "claude-3-7-sonnet", Label: "Claude 3.7 Sonnet", ContextWindowK: 200, MaxOutputK: 64,
		ThinkingType: "enabled", ReasoningEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium", Vision: true},
	{Prefix: "claude-3-5-sonnet", Label: "Claude 3.5 Sonnet", ContextWindowK: 200, MaxOutputK: 8, Vision: true},
	{Prefix: "claude-3-5-haiku", Label: "Claude 3.5 Haiku", ContextWindowK: 200, MaxOutputK: 8, Vision: true},

	// OpenAI
	{Prefix: "gpt-5", Label: "GPT-5", ContextWindowK: 400, MaxOutputK: 128,
		ThinkingType: "enabled", ReasoningEfforts: []string{"minimal", "low", "medium", "high"}, DefaultEffort: "medium", Vision: true},
	{Prefix: "gpt-4.1-mini", Label: "GPT-4.1 mini", ContextWindowK: 1047, MaxOutputK: 32, Vision: true},
	{Prefix: "gpt-4.1", Label: "GPT-4.1", ContextWindowK: 1047, MaxOutputK: 32, Vision: true},
	{Prefix: "gpt-4o-mini", Label: "GPT-4o mini", ContextWindowK: 128, MaxOutputK: 16, Vision: true},
	{Prefix: "gpt-4o", Label: "GPT-4o", ContextWindowK: 128, MaxOutputK: 16, Vision: true},
	{Prefix: "o4-mini", Label: "o4-mini", ContextWindowK: 200, MaxOutputK: 100,
		ThinkingType: "enabled", ReasoningEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
	{Prefix: "o3-mini", Label: "o3-mini", ContextWindowK: 200, MaxOutputK: 100,
		ThinkingType: "enabled", ReasoningEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
	{Prefix: "o3", Label: "o3", ContextWindowK: 200, MaxOutputK: 100,
		ThinkingType: "enabled", ReasoningEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},

	// DeepSeek
	{Prefix: "deepseek-reasoner", Label: "DeepSeek Reasoner", ContextWindowK: 128, MaxOutputK: 64, ThinkingType: "enabled"},
	{Prefix: "deepseek-chat", Label: "DeepSeek Chat", ContextWindowK: 128, MaxOutputK: 8},

	// Google
	{Prefix: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", ContextWindowK: 1048, MaxOutputK: 65, ThinkingType: "enabled", Vision: true},
	{Prefix: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", ContextWindowK: 1048, MaxOutputK: 65, ThinkingType: "enabled", Vision: true},
	{Prefix: "gemini-2.0-flash", Label: "Gemini 2.0 Flash", ContextWindowK: 1048, MaxOutputK: 8, Vision: true},

	// 中国大陆常见家族
	{Prefix: "qwen3", Label: "Qwen3", ContextWindowK: 128},
	{Prefix: "qwen-plus", Label: "Qwen Plus", ContextWindowK: 128},
	{Prefix: "qwen-max", Label: "Qwen Max", ContextWindowK: 32},
	{Prefix: "glm-4.6", Label: "GLM-4.6", ContextWindowK: 200},
	{Prefix: "glm-4.5", Label: "GLM-4.5", ContextWindowK: 128},
	{Prefix: "kimi-k2", Label: "Kimi K2", ContextWindowK: 256},
	{Prefix: "minimax-m2", Label: "MiniMax M2", ContextWindowK: 204},
}

func init() {
	// 最长前缀优先，避免 gpt-4o 抢走 gpt-4o-mini 的条目。
	sort.SliceStable(modelCatalogTable, func(i, j int) bool {
		return len(modelCatalogTable[i].Prefix) > len(modelCatalogTable[j].Prefix)
	})
}

// lookupModelCatalog 按模型名查公开参数。返回命中的条目和实际匹配到的前缀。
//
// 匹配规则：先看整名，再看最后一段（"openai/gpt-4o" 这类带厂商前缀的写法）。
// 找不到时 ok=false，调用方必须把"未知"如实显示出来，而不是补默认值。
func lookupModelCatalog(model string) (ModelCatalogEntry, string, bool) {
	raw := strings.ToLower(strings.TrimSpace(model))
	if raw == "" {
		return ModelCatalogEntry{}, "", false
	}
	candidates := []string{raw}
	if i := strings.LastIndex(raw, "/"); i >= 0 && i+1 < len(raw) {
		candidates = append(candidates, raw[i+1:])
	}
	for _, name := range candidates {
		for _, e := range modelCatalogTable {
			if strings.HasPrefix(name, e.Prefix) {
				return e, e.Prefix, true
			}
		}
	}
	return ModelCatalogEntry{}, "", false
}

// catalogSuggestion 把目录条目换算成 profile 字段的"建议值"，只给出确知的部分。
// 返回的 map 供前端直接用作表单初值；缺的键就是不知道，前端保持空。
func catalogSuggestion(model string) map[string]any {
	e, matched, ok := lookupModelCatalog(model)
	if !ok {
		return map[string]any{"model": model, "known": false}
	}
	out := map[string]any{"model": model, "known": true, "matched": matched}
	if e.Label != "" {
		out["label"] = e.Label
	}
	if e.ContextWindowK > 0 {
		out["context_window_k"] = e.ContextWindowK
	}
	if e.MaxOutputK > 0 {
		out["max_output_k"] = e.MaxOutputK
	}
	if e.ThinkingType != "" {
		out["thinking_type"] = e.ThinkingType
	}
	if len(e.ReasoningEfforts) > 0 {
		out["reasoning_efforts"] = e.ReasoningEfforts
	}
	if e.DefaultEffort != "" {
		out["reasoning_effort"] = e.DefaultEffort
	}
	if e.Vision {
		out["vision"] = true
	}
	return out
}
