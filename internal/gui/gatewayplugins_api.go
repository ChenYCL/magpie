package gui

import (
	"encoding/json"
	"net/http"

	"github.com/yetone/magpie/internal/gwplugin"
)

// gatewayPluginRoutes are the gateway plugins' API: programs of the user's
// own that every request goes through (internal/gwplugin), and what this
// process has seen of each.
//
// The page that draws them is the presets plugin's (a preset and a gateway
// plugin both shape what the vendor gets: one by configuration, one by
// code), and these routes are the core's, as a plugin package reaches the
// window through core routes alone.
func gatewayPluginRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/gateway-plugins", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, map[string]any{
			"plugins": gwplugin.Statuses(),
			// the agents' names, and the example the editor starts from
			"known": agentsKnown(),
			// what a plugin's own Try sends, and the two hooks' shapes
			"requestExample": gwplugin.Example()["request"],
			"usageExample":   gwplugin.Example()["usage"],
		})
	})
	mux.HandleFunc("POST /api/gateway-plugins/save", func(rw http.ResponseWriter, r *http.Request) {
		var p gwplugin.Plugin
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := gwplugin.Put(p); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		gwplugin.Forget(p.ID) // a change starts its health afresh
		saved, _ := gwplugin.Get(p.ID)
		writeJSON(rw, saved)
	})
	mux.HandleFunc("POST /api/gateway-plugins/remove", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := gwplugin.Remove(in.ID); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]bool{"ok": true})
	})
	// a test the plugin itself answers: magpie sends it a request of its
	// own, and says what came back, so a plugin is known to work before a
	// single agent's traffic goes through it
	mux.HandleFunc("POST /api/gateway-plugins/try", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Plugin gwplugin.Plugin `json:"plugin"`
			Body   string          `json:"body"`
			Agent  string          `json:"agent"`
			Model  string          `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := gwplugin.Try(r.Context(), in.Plugin, gwplugin.Request{
			Protocol: "chat", Model: in.Model, Agent: in.Agent,
		}, []byte(in.Body))
		if err != nil {
			writeJSON(rw, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(rw, map[string]any{"body": string(out)})
	})
}
