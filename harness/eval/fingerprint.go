// Package eval turns a replay into comparable, gate-able metrics.
//
// The owner's acceptance criterion for consistency (ADR-0001 decision 2) is
// deliberately narrow: same prompts + same policy, and the pass condition is
// "refusal wording and behaviour logic are identical" — NOT identical model
// weights. So the metrics here are chosen to be deterministic and diffable:
//
//   - which calls were BLOCKED (a set, must be identical);
//   - the refusal TEXT each block produced (byte-compared);
//   - terminal/stop reasons per turn (distribution, thresholded);
//   - the tool-call multiset (distribution, thresholded);
//   - a structural fingerprint of the whole trajectory (regression signal).
//
// Why fingerprints rather than pass/fail: binary assertions are useless for a
// non-deterministic workflow (published AgentAssay measurements: fingerprinting
// caught 86% of regressions, binary pass/fail caught 0%). A fingerprint change is
// not automatically a failure — it is a signal that a human must look.
package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Autumn-27/artex/harness/replay"
)

// GuardBlockMarker is the prefix the platform prepends to every intercepted tool
// result (guard.systemBlockMessage). Detecting it is how a replay identifies a
// platform block rather than a target-side error — the distinction the whole
// consistency check rests on, because an agent that mistakes a policy block for a
// WAF will try to evade it.
const GuardBlockMarker = "【ARTEX 平台管控·非目标防御】"

// OtherHookBlocks are the harness-level denials that are not DB-rule blocks but
// still belong to "the platform refused" (norma's execOne / guard paths).
var OtherHookBlocks = []string{
	"Blocked by hook:",
	"Tool call was not permitted:",
}

// BlockEvent is one refused call.
type BlockEvent struct {
	Revision int64  `json:"revision"`
	Tool     string `json:"tool"`
	Kind     string `json:"kind"`    // rule | hook | policy
	Message  string `json:"message"` // the exact text the model received
}

// Fingerprint is the structural identity of a run.
type Fingerprint struct {
	// Steps is the ordered structural skeleton: turn kind, item kind and tool name.
	// Deliberately excludes free text so it survives benign wording changes but
	// catches reordering, dropped tools and new/removed rounds.
	Steps []string `json:"steps"`
	// Tools is the multiset of tool names actually invoked.
	Tools map[string]int `json:"tools"`
	// TurnKinds counts turns by kind (planner/work/system).
	TurnKinds map[string]int `json:"turn_kinds"`
	// Turns and Items are counts, for a quick "did anything happen" check.
	Turns int `json:"turns"`
	Items int `json:"items"`
	// Hash is the digest of Steps — the regression key.
	Hash string `json:"hash"`
}

// Compute derives the fingerprint of a run.
func Compute(run *replay.Run) Fingerprint {
	fp := Fingerprint{
		Tools:     map[string]int{},
		TurnKinds: map[string]int{},
	}
	if run == nil {
		// Still emit a well-formed fingerprint (of an empty trajectory) so callers
		// never have to special-case the zero value.
		h := sha256.Sum256(nil)
		fp.Hash = hex.EncodeToString(h[:])
		return fp
	}
	fp.Turns = len(run.Turns)
	fp.Items = run.ItemCount()
	for _, t := range run.Turns {
		kind := t.Kind
		if kind == "" {
			kind = "legacy"
		}
		fp.TurnKinds[kind]++
		fp.Steps = append(fp.Steps, "turn:"+kind)
		for _, it := range t.Items {
			step := fmt.Sprintf("item:%s", it.Kind)
			if it.Tool != "" {
				step += ":" + it.Tool
				// Only an invocation counts towards the tool multiset; the result
				// carries the same tool name and would otherwise double-count.
				if it.Kind == "tool_use" {
					fp.Tools[it.Tool]++
				}
			}
			if it.IsError {
				step += ":err"
			}
			fp.Steps = append(fp.Steps, step)
		}
	}
	h := sha256.Sum256([]byte(strings.Join(fp.Steps, "\n")))
	fp.Hash = hex.EncodeToString(h[:])
	return fp
}

// Blocks extracts every refused call, in order, with the message the model saw.
//
// A block is recognised by the platform marker on an errored item. The message is
// kept verbatim (only newlines folded) because decision 2 demands byte-level
// wording equality.
func Blocks(run *replay.Run) []BlockEvent {
	if run == nil {
		return nil
	}
	out := make([]BlockEvent, 0, 4)
	for _, t := range run.Turns {
		for _, it := range t.Items {
			if !it.IsError {
				continue
			}
			text := it.Summary
			if text == "" {
				text = it.Detail
			}
			kind, msg, ok := classifyBlock(text)
			if !ok {
				continue
			}
			out = append(out, BlockEvent{
				Revision: t.Revision,
				Tool:     it.Tool,
				Kind:     kind,
				Message:  normalizeText(msg),
			})
		}
	}
	return out
}

func classifyBlock(text string) (kind, msg string, ok bool) {
	if idx := strings.Index(text, GuardBlockMarker); idx >= 0 {
		return "rule", text[idx:], true
	}
	for _, marker := range OtherHookBlocks {
		if idx := strings.Index(text, marker); idx >= 0 {
			return "hook", text[idx:], true
		}
	}
	return "", "", false
}

// normalizeText folds whitespace so the comparison is about wording, not about
// how a tool happened to wrap its output. Evidence of intent is preserved.
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	// Drop trailing empty lines, keep internal structure.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// RefusalTexts returns the distinct refusal messages, sorted for stable diffing.
func RefusalTexts(blocks []BlockEvent) []string {
	set := map[string]bool{}
	for _, b := range blocks {
		set[b.Message] = true
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// BlockedCalls returns the distinct (tool, kind) pairs that were refused, sorted.
// Comparing these sets answers "did the same calls get blocked as before?" — the
// first half of decision 2's acceptance.
func BlockedCalls(blocks []BlockEvent) []string {
	set := map[string]bool{}
	for _, b := range blocks {
		key := b.Kind + ":" + orTool(b.Tool)
		set[key] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orTool(t string) string {
	if t == "" {
		return "-"
	}
	return t
}

// TerminalReasons buckets the per-turn outcome from the last item of each turn.
//
// The runner records a terminal reason on a result item; without that item we fall
// back to "errored last item" vs "clean", which is enough to detect a systematic
// change in how runs end.
func TerminalReasons(run *replay.Run) map[string]int {
	out := map[string]int{}
	if run == nil {
		return out
	}
	for _, t := range run.Turns {
		if len(t.Items) == 0 {
			continue
		}
		last := t.Items[len(t.Items)-1]
		key := "clean"
		switch {
		case last.Kind == "result":
			key = "result"
		case last.IsError:
			key = "error"
		case last.Kind == "text":
			key = "text"
		}
		out[key]++
	}
	return out
}
