package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/modelprobe"
)

// modelProbeRoutes are the Model probe page's API: the plan, the last
// round's findings merged by model id, and the two ways to ask again — one
// model, or some.
//
// A probe is magpie's own request, written down in the request log as such
// (probelog.KindTest) and counted in no usage total. The standing round is
// the page's switch; a manual run works whether it is on or off.
func modelProbeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/model-probe", func(rw http.ResponseWriter, r *http.Request) {
		p, err := modelprobe.Load()
		if err != nil {
			fail(rw, err)
			return
		}
		last := modelprobe.Last()
		// which routing groups carry each id, and the order their members
		// fall back through: an id two groups carry, and a group whose
		// members answer differently, is the whole difference between "the
		// id works" and "a request for it lands"
		routes := map[string][]modelprobe.Route{}
		for _, row := range last.Rows {
			if rs := modelprobe.RoutesFor(row.ID); len(rs) > 0 {
				routes[row.ID] = rs
			}
		}
		out := map[string]any{
			"plan":    p,
			"every":   p.EveryDuration().String(),
			"results": last,
			"routes":  routes,
			// what a manual run may pick from: every exposed id, and the
			// providers serving it
			"ids": modelprobe.Exposed(),
			// how long a round's answers stand for, so the page can say one
			// is old
			"now": time.Now().UnixMilli(),
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/model-probe/save", func(rw http.ResponseWriter, r *http.Request) {
		var in modelprobe.Plan
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := modelprobe.Save(in); err != nil {
			fail(rw, err)
			return
		}
		p, err := modelprobe.Load()
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"plan": p, "every": p.EveryDuration().String()})
	})
	// a manual run: every exposed model when the body names none, else the
	// ones it names (provider/model pins an id to one provider)
	mux.HandleFunc("POST /api/model-probe/run", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Models    []string `json:"models"`
			Providers []string `json:"providers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		// a run of a thousand models takes longer than a browser waits: the
		// page asks one provider's or one model's at a time, and this bound
		// keeps a round from being cut off mid-way with nothing said
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		out, err := modelprobe.Run(ctx, modelprobe.DoD{Models: in.Models, Providers: in.Providers})
		if err != nil && err != modelprobe.ErrRunning {
			// a round already running is answered as such, with what is
			// known: the page shows the running state rather than an error
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"results": out, "running": err == modelprobe.ErrRunning})
	})
}
