package browser

// Thin aliases so manager.go reads without norma prefixes everywhere. Kept in one
// file so the SDK surface this package depends on is visible at a glance.

import (
	"encoding/json"

	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

type permContext = acperm.Context
type decision = acperm.Decision

func allowDecision() decision { return acperm.Allowed() }

func textBlockFor(s string) llm.ContentBlock { return llm.TextBlock(s) }

// firstText is the first text block of a result ("" when there is none).
func firstText(res actool.Result) string {
	for _, b := range res.Content {
		if b.Type == llm.BlockText {
			return b.Text
		}
	}
	return ""
}

// rawInput keeps Spec callbacks typed correctly.
func rawInput(in []byte) json.RawMessage { return json.RawMessage(in) }
