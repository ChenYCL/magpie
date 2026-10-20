package provider

import (
	"net/http"
	"testing"

	"github.com/yetone/magpie/internal/fingerprint"
	"github.com/yetone/magpie/internal/proxypool"
)

// save writes p the way the editor does, in a config folder of the test's.
func save(t *testing.T, p Provider) error {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return Save(p)
}

func TestASavedFingerprintIsKeptAndChecked(t *testing.T) {
	// a provider of the user's own, with a built-in profile: saved, kept,
	// and valid
	p := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "k", Fingerprint: fingerprint.Assign{Profile: "claude-code"}}
	if err := save(t, p); err != nil {
		t.Fatalf("a profile magpie ships was refused: %v", err)
	}
	n := normalize(p)
	if n.Fingerprint.Profile != "claude-code" {
		t.Fatalf("the fingerprint went with normalize: %+v", n.Fingerprint)
	}

	// an id magpie has no profile for is refused, with what it has
	p.Fingerprint = fingerprint.Assign{Profile: "nope"}
	if err := save(t, p); err == nil {
		t.Fatal("an unknown profile was taken")
	}

	// a profile of the user's own: its header names say nothing magpie
	// refuses, and the assignment is kept
	p.Fingerprint = fingerprint.Assign{Custom: &fingerprint.Profile{ID: "mine", Headers: map[string]string{" User-Agent ": " ua "}}}
	if err := save(t, p); err != nil {
		t.Fatalf("a custom profile: %v", err)
	}
	n = normalize(p)
	if n.Fingerprint.Custom == nil || n.Fingerprint.Custom.Headers["User-Agent"] != "ua" {
		t.Fatalf("the custom profile wasn't normalized: %+v", n.Fingerprint.Custom)
	}

	// a profile that names a header magpie signs with is refused
	p.Fingerprint = fingerprint.Assign{Custom: &fingerprint.Profile{ID: "bad", Headers: map[string]string{"Authorization": "x"}}}
	if err := save(t, p); err == nil {
		t.Fatal("a profile was allowed to sign the request")
	}
}

func TestASavedPoolIsKeptAndChecked(t *testing.T) {
	p := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "k",
		ProxyPool: proxypool.Pool{Mode: "random", Exits: []proxypool.Exit{{URL: "http://a"}, {URL: "http://b", Weight: 3}}}}
	if err := save(t, p); err != nil {
		t.Fatalf("a pool: %v", err)
	}
	n := normalize(p)
	if len(n.ProxyPool.Exits) != 2 || n.ProxyPool.Mode != "random" {
		t.Fatalf("the pool went with normalize: %+v", n.ProxyPool)
	}

	// an exit with no address is dropped, and a pool left with none is none
	n = normalize(Provider{ProxyPool: proxypool.Pool{Exits: []proxypool.Exit{{URL: "  "}}}})
	if n.ProxyPool.On() || len(n.ProxyPool.Exits) != 0 {
		t.Fatalf("an empty pool stayed: %+v", n.ProxyPool)
	}

	// two exits at one address is refused
	p.ProxyPool = proxypool.Pool{Exits: []proxypool.Exit{{URL: "http://a"}, {URL: "http://a"}}}
	if err := save(t, p); err == nil {
		t.Fatal("a pool with one exit twice was taken")
	}
}

func TestThePoolAsksForOneExitAndRestsIt(t *testing.T) {
	proxypool.ForgetAll()
	p := Provider{ID: "po", Key: "k", ProxyPool: proxypool.Pool{Mode: "round-robin", Exits: []proxypool.Exit{
		{URL: "http://a", Label: "hk"}, {URL: "http://b", Label: "us"},
	}}}
	req, _ := http.NewRequest("POST", "https://relay.example/v1/chat/completions", nil)

	// a request naming an exit goes through it, and the header goes no
	// further
	req.Header.Set(poolPinHeader, "us")
	choice, done, ok := poolPick(p, req)
	if !ok || choice != "http://b" {
		t.Fatalf("pinned: %q %v", choice, ok)
	}
	if req.Header.Get(poolPinHeader) != "" {
		t.Fatal("the pin header reached the vendor")
	}
	done(&http.Response{StatusCode: 200}, nil)

	// no pin: the rotation takes the next
	req.Header.Del(poolPinHeader)
	next, d, ok := poolPick(p, req)
	if !ok || (next != "http://a" && next != "http://b") {
		t.Fatalf("rotation: %q %v", next, ok)
	}
	d(&http.Response{StatusCode: 200}, nil)

	// a provider with no pool leaves the proxy as it was
	p.ProxyPool = proxypool.Pool{}
	if _, _, ok := poolPick(p, req); ok {
		t.Fatal("a provider with no pool picked one")
	}
	// a pool whose exits are all off is none either
	p.ProxyPool = proxypool.Pool{Exits: []proxypool.Exit{{URL: "http://a", Disabled: true}}}
	if _, _, ok := poolPick(p, req); ok {
		t.Fatal("a pool of disabled exits picked one")
	}
}
