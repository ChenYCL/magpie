package gateway

import (
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// wholeUnreadable is sego's new-api failing on the whole reply its
// upstream gave it, whose reasoning is a list of entries.
const wholeUnreadable = `{"error":{"message":"json: cannot unmarshal array into Go struct field ***.***.***.reasoning of type string","type":"bad_response_body","param":"","code":"bad_response_body"}}`

// A relay that cannot read the whole reply it was given — sego's new-api,
// on a model that thinks — streams the very same request without trouble:
// a client that asked for one body is answered streamed and given one, and
// the model is asked that way from then on.
func TestWholeReplyUnreadableAskedStreamed(t *testing.T) {
	fresh(t)
	s := &scripted{replies: []reply{
		{500, "application/json", wholeUnreadable},
		{200, "text/event-stream", sse(`data: {"id":"x","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `data: [DONE]`)},
	}}
	scriptedOn(t, "a", provider.Chat, s)
	code, body := post(t, "/v1/chat/completions", `{"model":"a/m","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || !strings.Contains(body, `"content":"ok"`) {
		t.Fatalf("%d %s", code, body)
	}
	// and from then on it is asked streamed, the whole reply no more
	code, body = post(t, "/v1/chat/completions", `{"model":"a/m","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || !strings.Contains(body, `"content":"ok"`) {
		t.Fatalf("second turn: %d %s", code, body)
	}
	if s.n != 3 {
		t.Fatalf("asked %d times, want 3 (one whole, then streamed each)", s.n)
	}
}

// A 500 that is not a relay failing to read the reply — the model's own
// fault — goes to the client as it came.
func TestUnreadableWholeOnlyForAnUnmarshalFault(t *testing.T) {
	fresh(t)
	s := &scripted{replies: []reply{{500, "application/json", `{"error":{"message":"upstream blew up"}}`}}}
	scriptedOn(t, "a", provider.Chat, s)
	code, body := post(t, "/v1/chat/completions", `{"model":"a/m","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 500 || !strings.Contains(body, "upstream blew up") {
		t.Fatalf("%d %s", code, body)
	}
}

// A client that asked for a stream was already given one: nothing is asked
// again, and the refusal goes as it came.
func TestUnreadableWholeNotForAStreamedAsk(t *testing.T) {
	fresh(t)
	s := &scripted{replies: []reply{{500, "application/json", wholeUnreadable}}}
	scriptedOn(t, "a", provider.Chat, s)
	code, body := post(t, "/v1/chat/completions", `{"model":"a/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 500 || !strings.Contains(body, "cannot unmarshal array") {
		t.Fatalf("%d %s", code, body)
	}
}
