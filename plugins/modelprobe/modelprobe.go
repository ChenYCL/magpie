// Package modelprobeplugin serves the Model probe page: which model ids
// answer at which providers, merged by id, checked on its own every half
// hour or when the user asks (internal/modelprobe).
//
// The page is what the plugin is: the round is the core's, started here
// when the plugin is on and ended with it, and the page's API is the core's
// too (/api/model-probe).
package modelprobeplugin

import (
	"github.com/yetone/magpie/internal/modelprobe"
	"github.com/yetone/magpie/internal/pluginrt"
)

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "modelprobe", API: "^1", Requires: []string{"gateway"}, Essential: false,
		Name: "Model probe", Sub: "Which model ids answer, at which providers: a standing check every half hour, and a run of one or several when you ask",
		One: "The model probe is on", Many: "The model probe is on",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "modelprobe"}},
		Client: []string{"modelprobe.css", "client.js", "i18n.js"},
		Apply: func(c *pluginrt.Ctx) error {
			modelprobe.Start()
			c.OnDispose(func() { modelprobe.Stop() })
			return nil
		},
	})
}
