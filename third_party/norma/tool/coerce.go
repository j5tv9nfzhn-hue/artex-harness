package tool

import (
	"encoding/json"
	"strconv"
	"strings"
)

// CoerceNumericParams rewrites string arguments into JSON numbers, but only for
// object properties whose schema declares type "number" or "integer".
//
// Models routinely emit {"pageId": "1"} for an integer field. Strict validation
// then rejects the call, the model retries with the same shape, and the turn burns
// out on a formatting mistake that carries no meaning: a real DevTools session
// failed three times in a row on "pageId: expected number" before giving up. The
// coercion is deliberately narrow — it only touches values the tool itself
// declared numeric, never changes an object into a string or vice versa, and any
// parse failure leaves the original bytes untouched so validation still reports the
// real problem.
func CoerceNumericParams(schema map[string]any, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || schema == nil {
		return raw
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return raw
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || len(body) == 0 {
		return raw
	}
	changed := false
	for key, val := range body {
		prop, ok := props[key].(map[string]any)
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			if isNumericType(prop["type"]) {
				if n, ok := parseNumber(v); ok {
					body[key] = n
					changed = true
				}
			}
		case []any:
			// Arrays of ids are the other common shape ("[1, 2]" typed as strings).
			items, _ := prop["items"].(map[string]any)
			if !isNumericType(items["type"]) {
				continue
			}
			out := make([]any, 0, len(v))
			touched := false
			for _, item := range v {
				if s, ok := item.(string); ok {
					if n, ok := parseNumber(s); ok {
						out = append(out, n)
						touched = true
						continue
					}
				}
				out = append(out, item)
			}
			if touched {
				body[key] = out
				changed = true
			}
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}

func isNumericType(t any) bool {
	switch v := t.(type) {
	case string:
		return v == "number" || v == "integer"
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && (s == "number" || s == "integer") {
				return true
			}
		}
	}
	return false
}

// parseNumber accepts plain decimal forms only: no hex, no underscores, no
// whitespace tricks.
func parseNumber(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 32 {
		return nil, false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	return nil, false
}
