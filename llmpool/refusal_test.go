package llmpool

import (
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func textMsg(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(s)}}
}

func TestDetectRefusals(t *testing.T) {
	hit := []string{
		// English: canonical refusal openers.
		"I'm sorry, but I can't help with hacking into that system.",
		"I cannot help with unauthorized access to this server.",
		"As an AI, I'm unable to assist with penetration testing.",
		"Sorry, I can't assist with that request.",
		"I am not allowed to help with attacking this target.",
		"I must refuse this request.",
		// Case-insensitive, punctuation-insensitive.
		"I CAN'T HELP WITH THAT.",
		// Chinese: refusal verb + help object, never a bare 无法/不能.
		"抱歉，我不能帮助你进行渗透测试。",
		"作为 AI，我无法协助执行这类操作。",
		"我无法协助你入侵该系统。",
		"对不起，我不能协助攻击目标。",
		"这项操作不被允许，我无法执行。",
	}
	for _, s := range hit {
		if res := Detect(textMsg(s)); !res.Refused {
			t.Errorf("Detect(%q) = not refused, want refused", s)
		}
	}
}

func TestDetectNonRefusals(t *testing.T) {
	miss := []string{
		// Ordinary failure text: "cannot" without a help object, "无法" bare.
		"目标不可达：连接失败（connection refused），请检查网络。",
		"工具报错：cannot connect to 10.0.0.1:8080",
		"Bash 执行失败：exit status 1",
		"I cannot complete the download because the network is down.",
		"目标返回 404，端点不存在。",
		"我无法连接目标主机，请稍后重试。",
		// A self-description without a refusal verb must not fire.
		"作为 AI，我可以帮你分析这段日志。",
		// Normal working text.
		"已完成端口扫描，发现 3 个开放端口。",
		"",
		"   ",
	}
	for _, s := range miss {
		if res := Detect(textMsg(s)); res.Refused {
			t.Errorf("Detect(%q) = refused (signals %v), want not refused", s, res.Signals)
		}
	}
}

// A reply that invoked a tool did work — even if its text quotes a refusal,
// the refusal is something the model is reporting, not doing.
func TestDetectIgnoresToolUseReplies(t *testing.T) {
	msg := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		llm.TextBlock("The target said: I'm sorry, but I can't help with that."),
		{Type: llm.BlockToolUse, ID: "toolu_1", Name: "Bash"},
	}}
	if res := Detect(msg); res.Refused {
		t.Errorf("reply with a tool call was judged a refusal (signals %v)", res.Signals)
	}
}

func TestDetectResultCarriesSignalsAndExcerpt(t *testing.T) {
	res := Detect(textMsg("I'm sorry, but I can't help with that."))
	if !res.Refused || len(res.Signals) == 0 {
		t.Fatalf("expected a refusal with signals, got %+v", res)
	}
	if res.Excerpt == "" || len([]rune(res.Excerpt)) > 201 {
		t.Fatalf("excerpt = %q, want non-empty and <= 200 runes", res.Excerpt)
	}
}
