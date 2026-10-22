// Package sandbox serves the Sandbox page: the box an agent may run
// untrusted work in (internal/sandbox, internal/gateway/sandbox_mcp.go).
//
// The plugin is a page and nothing else — no request hook. The box is off
// by default: a broker is a program the user starts beside magpie
// (`magpie sandbox serve`), and magpie itself never holds a container
// engine's socket. The page's API is the core's (/api/sandbox).
package sandbox

import "github.com/yetone/magpie/internal/pluginrt"

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "sandbox", API: "^1", Requires: []string{"gateway"}, Essential: false,
		Name: "Sandbox", Sub: "A box of its own for untrusted work: a command runs in a fresh container, not on this computer",
		One: "The sandbox is on", Many: "The sandbox is on",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "sandbox"}},
		Client: []string{"sandbox.css", "client.js", "i18n.js"},
	})
}
