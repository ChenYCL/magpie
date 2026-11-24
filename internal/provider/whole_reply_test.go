package provider

import "testing"

// A relay failing to read the whole reply its upstream gave it names the
// field it could not unmarshal; anything else is the vendor's own fault.
func TestWholeReplyUnreadable(t *testing.T) {
	for msg, want := range map[string]bool{
		`{"error":{"message":"json: cannot unmarshal array into Go struct field ***.***.***.reasoning of type string","type":"bad_response_body"}}`: true,
		`{"error":{"message":"json: cannot unmarshal object into Go struct field Body.choices of type string"}}`:                                    true,
		`{"error":{"message":"upstream blew up"}}`:                                      false,
		`{"error":{"message":"The ` + "`reasoning_content`" + ` must be passed back"}}`: false,
		`{"error":{"message":"cannot unmarshal array into Go value of type string"}}`:   false, // not a struct field
	} {
		if got := WholeReplyUnreadable([]byte(msg)); got != want {
			t.Errorf("%s: %v", msg, got)
		}
	}
}
