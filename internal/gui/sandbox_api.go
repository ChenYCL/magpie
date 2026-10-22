package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/sandbox"
)

// sandboxRoutes are the Sandbox page's API: the plan, whether the broker
// answers, and the two things the page may do about it — try a command, and
// restore the boxes.
//
// magpie holds no container engine's socket: the plan's broker is a
// program beside it (`magpie sandbox serve`), and everything here is a
// request to that program. A broker that isn't there is said as such, and
// nothing is started on magpie's behalf.
func sandboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sandbox", func(rw http.ResponseWriter, r *http.Request) {
		p, err := sandbox.Load()
		if err != nil {
			fail(rw, err)
			return
		}
		out := map[string]any{"plan": p}
		if p.On {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			running, max, err := sandbox.BrokerState(ctx, p)
			if err != nil {
				out["broker"] = err.Error()
			} else {
				out["up"] = true
				out["running"] = running
				out["max"] = max
			}
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/sandbox/save", func(rw http.ResponseWriter, r *http.Request) {
		var p sandbox.Plan
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := sandbox.Save(p); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		saved, _ := sandbox.Load()
		writeJSON(rw, saved)
	})
	// the page's Try: one command, in the box, with what came back
	mux.HandleFunc("POST /api/sandbox/try", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Command string `json:"command"`
			Image   string `json:"image"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := sandbox.Load()
		if err != nil {
			fail(rw, err)
			return
		}
		res, err := sandbox.NewClient().Run(r.Context(), p, sandbox.Exec{Command: in.Command, Image: in.Image})
		if err != nil {
			writeJSON(rw, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(rw, map[string]any{"result": res})
	})
	mux.HandleFunc("POST /api/sandbox/reset", func(rw http.ResponseWriter, r *http.Request) {
		p, err := sandbox.Load()
		if err != nil {
			fail(rw, err)
			return
		}
		n, err := sandbox.BrokerReset(r.Context(), p)
		if err != nil {
			writeJSON(rw, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(rw, map[string]any{"removed": n})
	})
}
