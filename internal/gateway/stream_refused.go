package gateway

import (
	"github.com/yetone/magpie/internal/provider"
)

// A backend that only streams (claude-relay-service's Responses endpoint,
// like Codex's own ChatGPT backend) turns a request that asks for one body
// away: "Stream must be set to true" (Cyrus_4433 on X). An agent that sent
// stream:false got that 400. The request is then translated on the same
// API, which always asks the upstream streamed and gives the client its
// whole reply (translate), and so from then on, as a custom tool turned
// away is (custom_tool.go).

// streamOnlyRefused is how unfit remembers model's backend answering only
// streamed requests.
func streamOnlyRefused(model string) string { return "stream only\x00" + model }

// refusesWhole is whether a 400's body says the request must ask for a
// stream, while the client's body asked for one whole reply.
func refusesWhole(proto provider.Protocol, body, reply []byte) bool {
	return (proto == provider.Chat || proto == provider.Responses) && !streamOf(body) && provider.StreamRequired(wordsOf(reply))
}

// unreadableWhole is whether a reply is a relay failing to read the whole
// reply it was given, while the client's body asked for one: sego's
// new-api answers a completed reply whose reasoning is a list of entries,
// not one string — "json: cannot unmarshal array into Go struct field
// …reasoning of type string", under its own type "bad_response_body" — and
// streams the very same request without trouble. Asked streamed and
// translated, it answers: as a backend that only streams is (refusesWhole).
func unreadableWhole(status int, proto provider.Protocol, body, reply []byte) bool {
	if (proto != provider.Chat && proto != provider.Responses) || streamOf(body) || status < 400 {
		return false
	}
	return provider.WholeReplyUnreadable(reply)
}
