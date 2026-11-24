package provider

import "github.com/yetone/magpie/internal/lazyre"

// A relay that reads an upstream's whole reply into a struct of its own
// fails there when a field comes in another shape, and answers that
// failure as its own error: sego's new-api, in front of a model that
// thinks, gives a completed reply whose reasoning is a list of typed
// entries rather than one string as
//
//	{"error":{"message":"json: cannot unmarshal array into Go struct field
//	***.***.***.reasoning of type string","type":"bad_response_body",…}}
//
// — a 500, which no retry clears and no request shape of magpie's own
// fixes: the same request streamed answers fine, as the relay reads a
// stream chunk by chunk and keeps no such struct. So a whole reply is not
// asked of it (gateway's unreadableWhole).
//
// Only Go's own unmarshal complaint counts, naming the field it could not
// read: an upstream's refusal of a request field is a 400 of its own and
// is learned elsewhere (gateway's refusedInMessages).
var wholeReplyUnreadable = lazyre.New(`(?i)cannot unmarshal (?:array|object|number|string|bool) into Go struct field \S* of type`)

// WholeReplyUnreadable is whether a reply's words are a relay failing to
// read the whole reply it was given.
func WholeReplyUnreadable(body []byte) bool {
	return wholeReplyUnreadable.Match(body)
}
