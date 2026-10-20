package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/presets"
)

// The Agent presets page's API: the presets magpie ships, a user's own, the
// agent a preset is given to, and what a Try says a preset would do.
func TestPresetsRoutes(t *testing.T) {
	sandboxHome(t)
	mux := http.NewServeMux()
	presetsRoutes(mux)

	type page struct {
		Presets []presets.Preset `json:"presets"`
		Default string           `json:"default"`
		Exclude []string         `json:"exclude"`
		Known   []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"known"`
		Recent []any `json:"recent"`
	}
	request := func(method, path, body string, out any) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		if out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
	}

	var p page
	request("GET", "/api/presets", "", &p)
	if len(p.Presets) < 4 {
		t.Fatalf("the shipped presets aren't listed: %d", len(p.Presets))
	}
	ids := map[string]bool{}
	for _, x := range p.Presets {
		ids[x.ID] = true
		if !x.Builtin {
			t.Errorf("%s isn't marked a built-in", x.ID)
		}
	}
	for _, want := range []string{"standard", "minimal", "ptc", "redteam", "reverse"} {
		if !ids[want] {
			t.Errorf("%s isn't shipped", want)
		}
	}
	// the agents magpie knows come with the page, so a binding names one
	if len(p.Known) == 0 {
		t.Error("no agents to bind to")
	}

	// a user's preset
	request("POST", "/api/presets/save", `{"id":"mine","name":"Mine","patches":[{"id":"a","kind":"prompt","action":"append","value":"<x>y</x>"}]}`, nil)
	request("GET", "/api/presets", "", &p)
	mine := false
	for _, x := range p.Presets {
		if x.ID == "mine" {
			mine = true
			if x.Builtin {
				t.Error("a saved preset came back as a built-in")
			}
		}
	}
	if !mine {
		t.Fatal("a saved preset isn't listed")
	}

	// a bad patch is refused with the reason
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/presets/save",
		strings.NewReader(`{"id":"bad","patches":[{"id":"a","kind":"tools","action":"append"}]}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "append") {
		t.Fatalf("a bad patch: %d %s", w.Code, w.Body)
	}

	// one preset for every agent, and the agents left out of it
	request("POST", "/api/presets/default", `{"preset":"mine"}`, nil)
	request("POST", "/api/presets/exclude", `{"agent":"codex","excluded":true}`, nil)
	request("GET", "/api/presets", "", &p)
	if p.Default != "mine" || len(p.Exclude) != 1 || p.Exclude[0] != "codex" {
		t.Fatalf("store %+v", p)
	}
	// a preset magpie doesn't have can't be the default
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/presets/default", strings.NewReader(`{"preset":"nope"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("defaulted to nothing: %d", w.Code)
	}
	// an agent left out goes back in
	request("POST", "/api/presets/exclude", `{"agent":"codex","excluded":false}`, nil)
	request("GET", "/api/presets", "", &p)
	if len(p.Exclude) != 0 {
		t.Fatalf("still left out: %+v", p.Exclude)
	}

	// a copy of a built-in is the user's, and resettable/removable
	var cp presets.Preset
	request("POST", "/api/presets/copy", `{"from":"minimal","name":"My minimal"}`, &cp)
	if cp.Builtin || cp.ID == "minimal" {
		t.Fatalf("copy %+v", cp)
	}
	request("POST", "/api/presets/remove", `{"id":"`+cp.ID+`"}`, nil)
	// a built-in isn't removable, its edit is resettable
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/presets/remove", strings.NewReader(`{"id":"standard"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a built-in was removed: %d", w.Code)
	}
	request("POST", "/api/presets/save", `{"id":"standard","sub":"edited","patches":[]}`, nil)
	request("POST", "/api/presets/reset", `{"id":"standard"}`, nil)
	request("GET", "/api/presets", "", &p)
	for _, x := range p.Presets {
		if x.ID == "standard" && x.Sub == "edited" {
			t.Fatal("reset kept the edit")
		}
	}

	// Try: what a preset would do to a body, before it is saved
	var tr struct {
		Body  string        `json:"body"`
		Stats presets.Stats `json:"stats"`
	}
	request("POST", "/api/presets/try", `{"preset":{"id":"t","patches":[{"id":"d","kind":"prompt","action":"drop","match":"skills"}]},"proto":"chat","body":"{\"model\":\"m\",\"messages\":[{\"role\":\"system\",\"content\":\"a\\n<skills>x</skills>\"},{\"role\":\"user\",\"content\":\"hi\"}]}"}`, &tr)
	if tr.Stats.Prompt.Applied != 1 || strings.Contains(tr.Body, "<skills>") {
		t.Fatalf("try: %+v %s", tr.Stats, tr.Body)
	}
}
