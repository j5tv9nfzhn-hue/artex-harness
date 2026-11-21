// Refusal detection for model content refusals.
//
// Background: the OpenAI adapter in upstream norma already converts the
// structured `refusal` field into a plain TextBlock (third_party/norma/llm/
// openai.go), so by the time a reply reaches this pool there is no typed
// refusal signal left — detection has to be text-based. That is also why the
// threshold here is deliberately conservative ("宁漏勿误判"): a false
// positive would burn a failover (and a reprompt) on a reply that was fine,
// while a false negative just means this request gets no fallback and the
// refusal surfaces to the caller as before.
//
// Rules:
//   - Only a pure-text reply with NO tool calls is ever judged. A reply that
//     invoked a tool did work, whatever its text says (e.g. a tool result
//     quoted back, or a model narrating a refusal it saw from the target).
//   - Empty text is not a refusal either (that is a different failure mode).
//   - A signal only counts when it carries refusal semantics — "cannot help /
//     unable to assist / not allowed" — never a bare "cannot / 无法", which
//     shows up in ordinary failure text ("cannot connect", "目标不可达").
package llmpool

import (
	"strings"
	"unicode"

	"github.com/Autumn-27/norma/llm"
)

// Result is the structured outcome of Detect: whether the message is a
// refusal, which signals fired, and the excerpt that fired them. Callers
// need more than a bool — the signals feed the refusal-rate eval and the
// logs, and the excerpt is what an operator reads to confirm the call.
type Result struct {
	Refused bool
	Signals []string // ids of the signals that matched, in list order
	Excerpt string   // whitespace-folded text, truncated to 200 runes
}

// refusalSignal is one strong refusal phrase. pattern is written in the same
// normalized form Detect matches against (see normalizeText); why explains
// why this phrase — and not a shorter substring of it — counts as a strong
// signal, so a future edit can tell a safe tightening from a risky loosening.
type refusalSignal struct {
	id      string
	pattern string // normalized; english matched on spaced text, chinese on compact text
	why     string
}

var refusalSignals = []refusalSignal{
	// --- English: explicit inability / unwillingness to help or comply. ---
	// "I can't help" is the canonical refusal opener; bare "can't" alone is
	// not a signal (it appears in "can't reach the target" and the like).
	{id: "en:cant-help", pattern: "i cant help", why: "explicit inability to help"},
	{id: "en:cannot-help", pattern: "i cannot help", why: "explicit inability to help"},
	{id: "en:cant-assist", pattern: "i cant assist", why: "explicit inability to assist"},
	{id: "en:cannot-assist", pattern: "i cannot assist", why: "explicit inability to assist"},
	// "I'm unable to" on its own is too broad ("unable to connect"), so the
	// signal requires the help/assist object that makes it a refusal.
	{id: "en:unable-to-help", pattern: "unable to help", why: "inability to help, with the object attached"},
	{id: "en:unable-to-assist", pattern: "unable to assist", why: "inability to assist, with the object attached"},
	{id: "en:unable-to-comply", pattern: "unable to comply", why: "inability to comply with the request"},
	{id: "en:cant-comply", pattern: "i cant comply", why: "explicit refusal to comply"},
	{id: "en:cannot-comply", pattern: "i cannot comply", why: "explicit refusal to comply"},
	// Apologetic refusals: the apology plus the refusal verb is the signal;
	// a bare "sorry" is not (models apologise for ordinary failures too).
	{id: "en:sorry-cant", pattern: "sorry but i cant", why: "apologetic refusal"},
	{id: "en:sorry-cannot", pattern: "sorry but i cannot", why: "apologetic refusal"},
	// Policy refusals: the model cites permission rather than capability.
	{id: "en:not-allowed", pattern: "not allowed to", why: "cites lack of permission, not capability"},
	{id: "en:must-refuse", pattern: "i must refuse", why: "explicit statement of refusal"},
	{id: "en:have-to-refuse", pattern: "i have to refuse", why: "explicit statement of refusal"},
	{id: "en:cant-provide-assistance", pattern: "i cant provide assistance", why: "explicit inability to assist"},
	{id: "en:not-able-to-help", pattern: "not able to help", why: "explicit inability to help"},
	// --- Chinese: same bar, refusal semantics, never a bare "无法/不能". ---
	// Deliberately NOT listed: "作为 AI/人工智能" on its own. It opens many
	// refusals, but it is only a self-description — a model can also say
	// "作为 AI，我可以帮你…" — so it carries no refusal semantics by itself.
	// Refusals that start that way still hit via the refusal verb below
	// ("我无法协助", "我不能帮助", …), which is what actually refuses.
	{id: "zh:unable-assist", pattern: "我无法协助", why: "explicit inability to assist"},
	{id: "zh:cant-help", pattern: "我不能帮助", why: "explicit inability to help"},
	{id: "zh:cant-assist", pattern: "我不能协助", why: "explicit inability to assist"},
	{id: "zh:unable-help", pattern: "我无法帮助", why: "explicit inability to help"},
	{id: "zh:cant-help-you", pattern: "我不能帮你", why: "explicit inability to help the user"},
	{id: "zh:sorry-cant", pattern: "抱歉我不能", why: "apologetic refusal"},
	{id: "zh:unable-provide-help", pattern: "我无法提供帮助", why: "explicit inability to help"},
	{id: "zh:cant-provide-help", pattern: "我不能提供帮助", why: "explicit inability to help"},
	// Policy refusals in Chinese: permission, not capability.
	{id: "zh:not-allowed", pattern: "不被允许", why: "cites lack of permission"},
	{id: "zh:cant-execute", pattern: "我不能执行", why: "explicit refusal to execute"},
	{id: "zh:unable-execute", pattern: "我无法执行", why: "explicit refusal to execute"},
}

// normalizeText lower-cases, drops apostrophes (so "can't" and "cant" match
// one pattern), turns punctuation into spaces and folds whitespace. It
// preserves word boundaries, which is what the English signals rely on.
func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "'", "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// compactText is normalizeText with all whitespace removed, for the Chinese
// signals: Chinese has no word boundaries, and models write "作为 AI" and
// "作为AI" interchangeably, so the match must not depend on spacing.
func compactText(s string) string {
	return strings.ReplaceAll(normalizeText(s), " ", "")
}

// Detect judges whether msg is a content refusal.
//
// Gate first, signals second: a reply with any tool call, or with no text at
// all, is never a refusal — the model did work, or there is nothing to judge.
func Detect(msg llm.Message) Result {
	if len(msg.ToolUses()) > 0 {
		return Result{}
	}
	text := msg.Text()
	if strings.TrimSpace(text) == "" {
		return Result{}
	}
	norm := normalizeText(text)
	padded := " " + norm + " " // word-boundary guard for the English signals
	compact := compactText(text)
	var hits []string
	for _, sig := range refusalSignals {
		if strings.HasPrefix(sig.id, "zh:") {
			if strings.Contains(compact, sig.pattern) {
				hits = append(hits, sig.id)
			}
			continue
		}
		if strings.Contains(padded, " "+sig.pattern+" ") || strings.HasPrefix(norm, sig.pattern+" ") || strings.HasSuffix(norm, " "+sig.pattern) {
			hits = append(hits, sig.id)
		}
	}
	if len(hits) == 0 {
		return Result{}
	}
	return Result{Refused: true, Signals: hits, Excerpt: excerpt(text)}
}

// excerpt folds whitespace and truncates to 200 runes, so logs and eval
// output stay readable when a refusal runs long.
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}
