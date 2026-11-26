package gateway

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yetone/magpie/internal/lazyre"
)

// tooFewTokens reads a provider's floor on the reply's length from the 400
// it sends when a request asks for less ("max_tokens must be greater than
// 2"). Apps checking a model is up ask for a token or two, which most
// providers answer and some turn away. A > may come JSON-escaped, as
// \u003e: the error is read from the body magpie re-encoded for the agent,
// or a vendor encodes its own that way (#260: "Expected a value >= 16").
var tooFewTokens = lazyre.New(`(?i)max_(?:completion_|output_)?tokens.{0,60}?(greater than|more than|larger than|at least|(?:>|\\u003e)=?)\s*(\d+)`)

// tokenFloor is the least a provider said it takes, or 0 when the error
// isn't about that.
func tokenFloor(msg []byte) int {
	m := tooFewTokens.FindSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(string(m[2]))
	if err != nil || n < 0 || n > 1024 {
		return 0
	}
	if sign := string(m[1]); strings.EqualFold(sign, "at least") || strings.HasSuffix(sign, "=") {
		return n
	}
	return n + 1
}

// tooManyTokens reads a provider's ceiling on the reply's length from the
// 400 it sends when a request asks for more ("The requested 'max_tokens'
// of 64000 exceeds the maximum allowed for this model, which is 32768").
// A course to a group sends one model's window (Claude Opus 5.5's 64K to
// Claude Code) to a member that takes less (a relay's DeepSeek, 32K), and
// the member is asked again for the most it gives. Its number is the
// model's own, not the request's, so the request is only asked again when
// it asked for more.
var tooManyTokens = lazyre.New(`(?i)max_(?:completion_|output_)?tokens.{0,80}?(?:exceeds?|exceeded|over|larger than|greater than|more than|maximum|max allowed).{0,60}?(?:which is|is|of|allowed for this model, which is|:|maximum)\s*(\d{2,7})`)

// tokenCap is the most a provider said it takes, or 0 when the error isn't
// about that.
func tokenCap(msg []byte) int {
	m := tooManyTokens.FindSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// withTokenCap lowers the reply's length the request asks for to cap,
// wherever the request's protocol keeps it; false when it asked for that
// little already, so the error was about something else.
func withTokenCap(body []byte, cap int) ([]byte, bool) {
	var q map[string]json.RawMessage
	if cap <= 0 || json.Unmarshal(body, &q) != nil {
		return nil, false
	}
	lower := func(m map[string]json.RawMessage, key string) bool {
		var n int
		if json.Unmarshal(m[key], &n) != nil || n <= cap {
			return false
		}
		m[key], _ = json.Marshal(cap)
		return true
	}
	lowered := false
	for _, k := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		lowered = lower(q, k) || lowered
	}
	var gc map[string]json.RawMessage // Gemini's
	if json.Unmarshal(q["generationConfig"], &gc) == nil && lower(gc, "maxOutputTokens") {
		q["generationConfig"], _ = json.Marshal(gc)
		lowered = true
	}
	if !lowered {
		return nil, false
	}
	b, err := marshalJSON(q)
	return b, err == nil
}

// withTokenFloor raises the reply's length the request asks for to floor,
// wherever the request's protocol keeps it; false when it asked for that
// much already, so the error was about something else.
func withTokenFloor(body []byte, floor int) ([]byte, bool) {
	var q map[string]json.RawMessage
	if floor <= 0 || json.Unmarshal(body, &q) != nil {
		return nil, false
	}
	raise := func(m map[string]json.RawMessage, key string) bool {
		var n int
		if json.Unmarshal(m[key], &n) != nil || n >= floor {
			return false
		}
		m[key], _ = json.Marshal(floor)
		return true
	}
	raised := false
	for _, k := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		raised = raise(q, k) || raised
	}
	var gc map[string]json.RawMessage // Gemini's
	if json.Unmarshal(q["generationConfig"], &gc) == nil && raise(gc, "maxOutputTokens") {
		q["generationConfig"], _ = json.Marshal(gc)
		raised = true
	}
	if !raised {
		return nil, false
	}
	b, err := marshalJSON(q)
	return b, err == nil
}
