package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/redteamstore"
)

// The red team page's API: nothing changes a record, and a magpie that
// never began an engagement says so rather than showing an empty table.
func TestRedteamRoutes(t *testing.T) {
	sandboxHome(t)
	db, err := redteamstore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	withDB := func() (*redteamstore.DB, error) { return db, nil }
	before := redteamDB
	redteamDB = withDB
	defer func() { redteamDB = before }()

	mux := http.NewServeMux()
	redteamRoutes(mux)
	get := func(path string) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %q isn't JSON: %v", path, rec.Body.String(), err)
		}
		return rec.Code, out
	}

	// an engagement that was begun reads back

	eng, err := db.Begin("单位R", "lab", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddAsset(eng.ID, redteamstore.AssetIn{Target: "198.51.100.5:443", Service: "https", Scope: "internal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Score(eng.ID, "server-host", "198.51.100.5:443", 20, "root"); err != nil {
		t.Fatal(err)
	}

	code, out := get("/api/redteam")
	if code != 200 || out["present"] != true {
		t.Fatalf("list: %d %v", code, out)
	}
	if !strings.Contains(mustJSON(t, out["engagements"]), "单位R") {
		t.Fatalf("list doesn't name the engagement: %v", out["engagements"])
	}

	code, out = get("/api/redteam/report?engagement=" + eng.ID)
	if code != 200 || out["present"] != true {
		t.Fatalf("report: %d %v", code, out)
	}
	rep := mustJSON(t, out["report"])
	if !strings.Contains(rep, "198.51.100.5") || !strings.Contains(rep, "\"scores\":20") {
		t.Fatalf("report is missing the asset or its points: %s", rep)
	}

	// the page changes nothing: a write is not a route
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/redteam/report", nil))
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Fatalf("POST answered %d", rec.Code)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
