package gui

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/presets"
	"github.com/yetone/magpie/internal/proxypool"
	"github.com/yetone/magpie/internal/provider"
)

// presetsRoutes are the Agent presets page's API: the presets magpie ships
// and the user's, which an agent gets, and the patches of each.
//
// The page itself is drawn by the presets plugin's client.js; these routes
// are the core's, since a plugin package reaches the window through core
// routes alone (internal/gui links no plugin package:
// TestPluginsImportOnlyCore).
//
// What the last requests did comes through gateway.PresetsRecent, which the
// presets plugin fills: core reaches no plugin package.
func presetsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/presets", func(rw http.ResponseWriter, r *http.Request) {
		s := presets.Load()
		writeJSON(rw, map[string]any{
			"presets": presets.All(),
			"agents":  s.Agents,
			"default": s.Default,
			"kinds":   presets.Kinds,
			"isolate": presets.Isolates(),
			// the agents magpie knows, so a binding names one
			"known":  agentsKnown(),
			"recent": recentOrEmpty(gateway.PresetsRecent),
		})
	})
	mux.HandleFunc("POST /api/presets/save", func(rw http.ResponseWriter, r *http.Request) {
		var p presets.Preset
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := presets.Put(p); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		saved, _ := presets.Get(p.ID)
		writeJSON(rw, saved)
	})
	mux.HandleFunc("POST /api/presets/copy", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ From, ID, Name string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := presets.Copy(in.From, in.ID, in.Name)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, p)
	})
	mux.HandleFunc("POST /api/presets/reset", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := presets.Reset(in.ID); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/presets/remove", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := presets.Remove(in.ID); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]bool{"ok": true})
	})
	// which preset an agent gets ("" the default), or the default itself
	mux.HandleFunc("POST /api/presets/bind", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Agent   string `json:"agent"`
			Preset  string `json:"preset"`
			Default bool   `json:"default"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		var err error
		if in.Default {
			err = presets.SetDefault(in.Preset)
		} else {
			err = presets.Bind(in.Agent, in.Preset)
		}
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]bool{"ok": true})
	})
	// what a preset does to a sample body, before it is saved: the page's
	// Try, so a patch is explained and not guessed
	mux.HandleFunc("POST /api/presets/try", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Preset presets.Preset `json:"preset"`
			Proto  string         `json:"proto"`
			Body   string         `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, st := presets.Apply(in.Preset, in.Proto, []byte(in.Body))
		writeJSON(rw, map[string]any{"body": string(out), "stats": st})
	})
}

// recentOrEmpty is the plugin's runs, or nothing when no plugin is on to
// keep any.
func recentOrEmpty(recent func() any) any {
	if recent == nil {
		return []any{}
	}
	return recent()
}

// knownAgent is one agent magpie knows, for a binding's menu.
type knownAgent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// agentsKnown is the agents magpie knows.
func agentsKnown() []knownAgent {
	all := agent.All()
	out := make([]knownAgent, 0, len(all))
	for _, a := range all {
		out = append(out, knownAgent{ID: a.ID, Name: a.Name})
	}
	return out
}

// poolRoutes are a provider's proxy pool: its exits, this process's health
// of each, and an exit rested by hand.
//
// The pool itself is saved with the provider (providers.json's proxyPool,
// through the provider editor's own save): these routes only read and rest.
func poolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/providers/{id}/pool", func(rw http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, err := provider.Find(id)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(rw, map[string]any{
			"pool":  p.ProxyPool,
			"exits": proxypool.Snapshot(p.ID, p.ProxyPool),
			"modes": proxypool.Modes,
		})
	})
	mux.HandleFunc("POST /api/providers/{id}/pool/rest", func(rw http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var in struct {
			URL  string `json:"url"`
			Rest string `json:"rest"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if in.URL == "" {
			http.Error(rw, "an exit's url is needed", http.StatusBadRequest)
			return
		}
		proxypool.Rest(id, in.URL, poolRest(in.Rest))
		writeJSON(rw, map[string]bool{"ok": true})
	})
}

// poolRest is a rest as the editor writes it ("30s", "5m"), a minute for
// one that isn't a length of time.
func poolRest(s string) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return time.Minute
}
