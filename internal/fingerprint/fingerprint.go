// Package fingerprint is the per-provider request fingerprint: the
// headers magpie's requests to a vendor go out with, so a relay that
// expects one client sees that client (#1851's next step, which today
// passes a client's own User-Agent on, or sends magpie's, and nothing
// else).
//
// A profile is a set of headers laid over what the gateway already set
// for the vendor, by a provider's Fingerprint (providers.json), or by the
// provider's preset when the user picked a profile for it. It is applied
// last, after the vendor's own signing, so a profile's header wins over
// magpie's default and over the agent's own passed-on one — the user's
// word about what the vendor should see.
//
// What a profile never changes:
//   - a signed-in subscription's account request, which the vendor's own
//     client binary sends (Claude Code, Codex): the profile is the
//     user's for providers of their own, and those go out as the account
//     client signs them.
//   - the body: a fingerprint of the headers alone is what relays check
//     first; BodyFingerprint is the switch that also reshapes the body.
//
// Built-in profiles are code (Builtins). A profile the user saved in
// providers.json's Fingerprint.Custom is theirs and may carry any header.
package fingerprint

import (
	"fmt"
	"sort"
	"strings"
)

// Profile is one client's headers, as a vendor's relay expects them.
type Profile struct {
	// ID names the profile in a provider's Fingerprint.Profile.
	ID string `json:"id"`
	// Name is what the editor shows.
	Name string `json:"name,omitempty"`
	// Sub says what the profile imitates, in the editor.
	Sub string `json:"sub,omitempty"`
	// Headers go out as they are: a vendor's own header, or one of the
	// client's. A header magpie sets for the vendor (Content-Type,
	// Authorization) is left as magpie set it unless the profile names it
	// in Override.
	Headers map[string]string `json:"headers,omitempty"`
	// Override are the headers the profile may replace even where magpie
	// sets one itself: Content-Type, Authorization, Host are refused.
	// A User-Agent needs no entry here.
	Override []string `json:"override,omitempty"`
	// Drop are headers the request would otherwise carry, by canonical
	// name (X-Stainless-*, stripped of its value). They go before the
	// profile's own are set.
	Drop []string `json:"drop,omitempty"`
	// Lock keeps the profile's headers on every request, including the
	// ones a signed-in account signs (default: those keep their client's).
	Lock bool `json:"lock,omitempty"`
}

// Assign is a provider's fingerprint: which profile, and whether the body
// is reshaped with it.
type Assign struct {
	// Profile is a Builtins id or one of Custom's.
	Profile string `json:"profile,omitempty"`
	// Custom is the user's own profile, saved beside the assignment so
	// it travels with providers.json.
	Custom *Profile `json:"custom,omitempty"`
	// Body also shapes the body to the profile's client: no "user" field,
	// no stream_options, and the metadata magpie adds. Off by default:
	// a body that differs from what the agent sent is harder to explain
	// in a bug report.
	Body bool `json:"body,omitempty"`
	// Disabled leaves the profile saved but not applied.
	Disabled bool `json:"disabled,omitempty"`
}

// Locked says the profile's headers stay on a signed-in account's request
// too, as its own client's usually are.
func (a Assign) Locked() bool {
	prof, ok := a.Resolve()
	return ok && prof != nil && prof.Lock
}

// On says the assignment applies.
func (a Assign) On() bool {
	if a.Disabled {
		return false
	}
	_, ok := a.Resolve()
	return ok
}

// Resolve is the assignment's profile: its own custom one, else a
// built-in, else none.
func (a Assign) Resolve() (*Profile, bool) {
	if a.Custom != nil {
		p := *a.Custom
		return &p, true
	}
	if p := Get(a.Profile); p != nil {
		return p, true
	}
	return nil, false
}

// Builtins are the profiles magpie ships. They are the clients vendors'
// relays name in their refusals ("only accessible via the official Claude
// CLI", "This account only allows Codex official clients", "only
// available via Cline product surfaces"), and the generic one.
//
// The User-Agents are the clients' own as they were read from their
// traffic (client-shapes.json keeps the last one magpie saw); a version
// is a real one, not a placeholder, since a relay that parses it may
// refuse a version it has never seen.
func Builtins() []Profile { return builtins }

var builtins = []Profile{
	{
		ID: "claude-code", Name: "Claude Code", Sub: "claude-cli, as Anthropic's own CLI sends it",
		Headers: map[string]string{
			"User-Agent":                            "claude-cli/2.1.300 (external, cli)",
			"X-App":                                 "cli",
			"anthropic-version":                     "2023-06-01",
			"anthropic-dangerous-direct-browser-access": "true",
			"X-Stainless-Lang":                      "js",
			"X-Stainless-Package-Version":           "0.128.0",
			"X-Stainless-OS":                        "MacOS",
			"X-Stainless-Arch":                      "arm64",
			"X-Stainless-Retry-Count":               "0",
		},
	},
	{
		ID: "codex-cli", Name: "Codex CLI", Sub: "codex_cli_rs, as OpenAI's Codex sends it",
		Headers: map[string]string{
			"User-Agent":  "codex_cli_rs/0.58.0 (Linux 6.8.0; x86_64) magpie-fingerprint",
			"originator":  "codex_cli_rs",
			"OpenAI-Beta": "responses=experimental",
		},
	},
	{
		ID: "opencode", Name: "OpenCode", Sub: "opencode, as its CLI sends it",
		Headers: map[string]string{
			"User-Agent": "opencode/1.0.0",
			"X-App":      "opencode",
		},
	},
	{
		ID: "cline", Name: "Cline", Sub: "Cline's desktop app, for Cline's free models",
		Headers: map[string]string{
			"User-Agent": "Cline/3.2.0 (Linux; x64)",
			"X-App":      "cline",
		},
	},
	{
		ID: "generic", Name: "magpie (no fingerprint)", Sub: "magpie's own User-Agent, as before",
		Headers: map[string]string{},
	},
}

// Get is the built-in profile with id, nil for none.
func Get(id string) *Profile {
	for i := range builtins {
		if builtins[i].ID == id {
			p := builtins[i]
			return &p
		}
	}
	return nil
}

// Ids lists the built-in profiles' ids, sorted.
func Ids() []string {
	ids := make([]string, 0, len(builtins))
	for _, p := range builtins {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

// forbidden are the headers a profile never replaces: the body's type, the
// vendor's signing (which the account's client sets last), and the host
// the request goes to.
var forbidden = map[string]bool{
	"Content-Type":  true,
	"Content-Length": true,
	"Authorization": true,
	"Host":          true,
	"Connection":    true,
	"Transfer-Encoding": true,
}

// Canonical is a header name canonicalised, and whether a profile may set
// it at all.
func Canonical(k string) (string, error) {
	c := canonical(k)
	if c == "" {
		return "", fmt.Errorf("a header needs a name")
	}
	if forbidden[c] {
		return "", fmt.Errorf("%s is magpie's own and a fingerprint never replaces it", c)
	}
	if strings.HasPrefix(c, "X-Magpie-") {
		return "", fmt.Errorf("%s tells magpie about the request and goes no further", c)
	}
	return c, nil
}

// canonical is http.CanonicalHeaderKey without importing net/http here:
// the same rule (each dash-separated word capitalised).
func canonical(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	b := []byte(strings.ToLower(k))
	up := true
	for i, c := range b {
		if c == '-' || c == '_' {
			up = true
			continue
		}
		if up && c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
		up = false
	}
	return string(b)
}

// Valid says why a user's profile can't be saved, nil when it can.
func (p Profile) Valid() error {
	if strings.TrimSpace(p.ID) == "" && p.ID != "" {
		return fmt.Errorf("a profile needs an id")
	}
	for k := range p.Headers {
		if _, err := Canonical(k); err != nil {
			return err
		}
	}
	for _, k := range p.Override {
		if _, err := Canonical(k); err != nil {
			return err
		}
	}
	for _, k := range p.Drop {
		if _, err := Canonical(k); err != nil {
			return err
		}
	}
	return nil
}

// DropPattern matches a Drop entry against a header name: "*" for any,
// else a prefix ending in "*" ("X-Stainless-*") or the whole name.
func DropPattern(entry, name string) bool {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return false
	}
	if entry == "*" {
		return true
	}
	if strings.HasSuffix(entry, "*") {
		return strings.HasPrefix(strings.ToLower(name), strings.ToLower(strings.TrimSuffix(entry, "*")))
	}
	return strings.EqualFold(entry, name)
}
