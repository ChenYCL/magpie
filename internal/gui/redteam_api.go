package gui

import (
	"net/http"
	"os"

	"github.com/yetone/magpie/internal/redteamstore"
)

// redteamRoutes are the Red team page's API: read-only whatever an
// engagement holds. A record is written by the engagement itself, through
// the MCP server at gateway.RedteamMCPPath, so nothing here changes one:
// a page that could edit a finding would let a report say a thing that
// never happened.
//
// There is no route while no engagement was ever begun: a magpie that
// never ran one opens no database, and the page says so rather than
// showing an empty table that reads as "nothing was found".

// redteamDB is the store the page reads, opened on the first read (a
// test's own in its place).
var redteamDB = func() (*redteamstore.DB, error) {
	if _, err := os.Stat(redteamstore.Path()); err != nil {
		return nil, err
	}
	return redteamstore.Open("")
}

func redteamRoutes(mux *http.ServeMux) {
	list := func(rw http.ResponseWriter, r *http.Request) {
		db, err := redteamDB()
		if err != nil {
			writeJSON(rw, map[string]any{
				"engagements": []any{}, "present": false,
				"why": "no red team engagement has been begun on this magpie: the database is made by the first redteam_engagement_begin call",
			})
			return
		}
		engs, err := db.Engagements()
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"present": true, "engagements": engs})
	}
	mux.HandleFunc("GET /api/redteam", list)
	mux.HandleFunc("GET /api/redteam/report", func(rw http.ResponseWriter, r *http.Request) {
		db, err := redteamDB()
		if err != nil {
			writeJSON(rw, map[string]any{"present": false,
				"why": "no red team engagement has been begun on this magpie"})
			return
		}
		rep, err := db.Report(r.URL.Query().Get("engagement"))
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"present": true, "report": rep})
	})
}
