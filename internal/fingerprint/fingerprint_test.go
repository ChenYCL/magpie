package fingerprint

import (
	"strings"
	"testing"
)

func TestBuiltinsAreValidAndNamedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Builtins() {
		if p.ID == "" || p.Name == "" {
			t.Errorf("%+v has no id or name", p)
		}
		if seen[p.ID] {
			t.Errorf("%s is in Builtins twice", p.ID)
		}
		seen[p.ID] = true
		if err := p.Valid(); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
	if len(Ids()) != len(Builtins()) {
		t.Error("Ids and Builtins disagree")
	}
}

func TestAGenericProfileHasNoHeaders(t *testing.T) {
	p := Get("generic")
	if p == nil || len(p.Headers) != 0 {
		t.Fatalf("generic %+v", p)
	}
	if Get("nope") != nil {
		t.Fatal("a profile magpie doesn't ship was found")
	}
}

func TestAssignResolvesItsProfile(t *testing.T) {
	a := Assign{Profile: "claude-code"}
	p, ok := a.Resolve()
	if !ok || p.ID != "claude-code" {
		t.Fatalf("%+v %v", p, ok)
	}
	if !a.On() || a.Locked() {
		t.Fatal("a built-in assignment is on and not locked")
	}

	// a custom one wins over the id it names
	a = Assign{Profile: "claude-code", Custom: &Profile{ID: "mine", Headers: map[string]string{"User-Agent": "x"}}}
	if p, ok := a.Resolve(); !ok || p.ID != "mine" {
		t.Fatalf("custom %+v", p)
	}
	// disabled is off
	if (Assign{Profile: "claude-code", Disabled: true}).On() {
		t.Fatal("a disabled assignment is on")
	}
	// an id magpie doesn't know is off, and says nothing
	if (Assign{Profile: "nope"}).On() {
		t.Fatal("an unknown profile is on")
	}
}

func TestCanonicalRefusesWhatMagpieOwns(t *testing.T) {
	for _, k := range []string{"Authorization", "content-type", "Host", "X-Magpie-Conversation", ""} {
		if _, err := Canonical(k); err == nil {
			t.Errorf("%q was taken", k)
		}
	}
	cases := map[string]string{
		"user-agent":        "User-Agent",
		"x-stainless-lang":  "X-Stainless-Lang",
		"anthropic-version": "Anthropic-Version",
		"x_app":             "X_App",
	}
	for in, want := range cases {
		if got, err := Canonical(in); err != nil || got != want {
			t.Errorf("Canonical(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestDropPattern(t *testing.T) {
	cases := []struct {
		entry, name string
		want        bool
	}{
		{"X-Stainless-*", "X-Stainless-Lang", true},
		{"x-stainless-*", "X-Stainless-Lang", true},
		{"User-Agent", "user-agent", true},
		{"User-Agent", "X-Other", false},
		{"*", "anything", true},
		{"", "anything", false},
	}
	for _, c := range cases {
		if got := DropPattern(c.entry, c.name); got != c.want {
			t.Errorf("DropPattern(%q,%q)=%v want %v", c.entry, c.name, got, c.want)
		}
	}
}

func TestAProfileNeverSetsASignedHeader(t *testing.T) {
	p := Profile{ID: "x", Headers: map[string]string{
		"Authorization": "Bearer evil",
		"User-Agent":    "ua",
	}}
	if err := p.Valid(); err == nil || !strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("a profile was allowed to sign the request: %v", err)
	}
}

func TestALockedProfileSaysSo(t *testing.T) {
	a := Assign{Profile: "claude-code"}
	if a.Locked() {
		t.Fatal("not locked")
	}
	a.Custom = &Profile{ID: "m", Lock: true}
	if !a.Locked() {
		t.Fatal("a locked custom profile isn't")
	}
}
